package ytdlp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const exeName = "yt-dlp"

var client = http.Client{Timeout: 5 * time.Second}

type GHDownloadData struct {
	TagName string `json:"tag_name"`
}

type YTDLPInstance struct {
	bPath string
}

type YTDLPVideoInfo struct {
	Id        string `json:"id"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Duration  uint   `json:"duration"`
}

func NewInstance(binPath string) (*YTDLPInstance, error) {
	if binPath == "" {
		return nil, errors.New("invalid binary path")
	}
	return &YTDLPInstance{bPath: binPath}, nil
}

func (inst YTDLPInstance) Execute(url string, args ...string) error {
	if url == "" {
		return errors.New("empty url")
	}
	args = slices.Insert(args, 0, url)
	cmd := exec.Command(inst.bPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New("yt-dlp error: \n" + fmt.Sprint(err) + " | " + string(out))
	}
	return nil
}

func (inst YTDLPInstance) ExecuteStdout(url string, args ...string) (io.Reader, error) {
	if url == "" {
		return nil, errors.New("empty url")
	}
	args = slices.Insert(args, 0, url)
	cmd := exec.Command(inst.bPath, args...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}
	go func() {
		defer pw.Close()
		cmd.Wait()
	}()
	return pr, nil
}

func (inst YTDLPInstance) DumpStdout(url string, args ...string) (string, error) {
	if url == "" {
		return "", errors.New("empty url")
	}
	cmd := exec.Command(inst.bPath, append(args, url)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (inst YTDLPInstance) GetVideoInfo(query string) (*YTDLPVideoInfo, error) {
	args := append(make([]string, 0), "ytsearch:"+query, "-s", "-O", "%(.{id,title,thumbnail,duration})#j")
	cmd := exec.Command(inst.bPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.New("yt-dlp error: \n" + fmt.Sprint(err) + " | " + string(out))
	}
	vi, err := decodeVideoInfo(string(out))
	if err != nil {
		return nil, errors.New("failed to decode video info: " + err.Error())
	}
	return vi, nil
}

func decodeVideoInfo(stdout string) (*YTDLPVideoInfo, error) {
	d := json.NewDecoder(strings.NewReader(stdout))
	vi := new(YTDLPVideoInfo)
	err := d.Decode(vi)
	if err != nil {
		return nil, err
	}
	return vi, nil
}

func (inst YTDLPInstance) ExecuteStream(url string, args []string) (io.Reader, error) {
	args = slices.Insert(args, 0, url)
	args = append(args, "-o", "-", "--newline")
	cmd := exec.Command(inst.bPath, args...)

	stdoutRd, stdoutW := io.Pipe()
	stderrRd, stderrW := io.Pipe()

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		defer stdoutW.Close()
		cmd.Wait()
	}()

	// blocks return until yt-dlp has started downloading or has errored
	ytErrCh := make(chan error)
	go func() {
		stderrLineScanner := bufio.NewScanner(stderrRd)
		for stderrLineScanner.Scan() {
			const downloadPrefix = "[download]"
			const errorPrefix = "ERROR: "
			line := stderrLineScanner.Text()
			if strings.HasPrefix(line, downloadPrefix) {
				break
			} else if strings.HasPrefix(line, errorPrefix) {
				ytErrCh <- errors.New(line[len(errorPrefix):])
				return
			}
		}
		ytErrCh <- nil
		_, _ = io.Copy(io.Discard, stderrRd)
	}()
	return stdoutRd, <-ytErrCh
}

func GetGithubReleases(page, entries int) ([]GHDownloadData, error) {
	url := fmt.Sprintf("https://api.github.com/repos/yt-dlp/yt-dlp/releases?page=%d&per_page=%d", page, entries)
	r, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	var d []GHDownloadData
	dErr := json.NewDecoder(r.Body).Decode(&d)
	if dErr != nil {
		return nil, dErr
	}
	return d, nil
}

func DownloadLatestFromGithub(path string) error {
	r, err := GetGithubReleases(1, 1)
	if err != nil {
		return err
	}
	v := r[0].TagName
	err = DownloadFromGithub(path, v)
	if err != nil {
		return fmt.Errorf("failed to download bin from GitHub releases: %v", err)
	}
	return nil
}

func DownloadFromGithub(path, version string) error {
	url := fmt.Sprintf("https://github.com/yt-dlp/yt-dlp/releases/download/%s/%s", version, exeName)
	if err := downloadFile(path, url); err != nil {
		return err
	}
	err := setExecPermission(path)
	return err
}

func downloadFile(path, url string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(out, res.Body)
	if err != nil {
		return err
	}
	return nil
}

func setExecPermission(fpath string) error {
	stat, err := os.Stat(fpath)
	if err != nil {
		return err
	}
	m := stat.Mode()
	return os.Chmod(fpath, m|0111)
}
