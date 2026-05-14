package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/minio/selfupdate"
)

const repo = "hsleedevelop/bdpeer"

var ErrAlreadyLatest = errors.New("already at latest version")

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Do fetches the latest release and applies it in place.
// Returns the new version tag on success, ErrAlreadyLatest if already current.
func Do(current string) (string, error) {
	rel, err := fetchLatest()
	if err != nil {
		return "", err
	}

	tag := rel.TagName
	bare := strings.TrimPrefix(tag, "v")
	if bare == current || tag == current || "v"+current == tag {
		return "", ErrAlreadyLatest
	}

	assetName := fmt.Sprintf("bdpeer_%s_%s_%s.tar.gz", bare, runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return "", fmt.Errorf("릴리즈에서 %s 아셋을 찾을 수 없습니다", assetName)
	}

	dlResp, err := http.Get(downloadURL) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("다운로드 실패: %w", err)
	}
	defer dlResp.Body.Close()

	binary, err := extractFromTarGz(dlResp.Body, "bdpeer")
	if err != nil {
		return "", fmt.Errorf("압축 해제 실패: %w", err)
	}

	if err := selfupdate.Apply(binary, selfupdate.Options{}); err != nil {
		return "", fmt.Errorf("업데이트 적용 실패: %w", err)
	}
	return tag, nil
}

// LatestTag returns the latest release tag from GitHub without applying anything.
func LatestTag() (string, error) {
	rel, err := fetchLatest()
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func fetchLatest() (*githubRelease, error) {
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest") //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("GitHub API 요청 실패: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 응답 오류: %s", resp.Status)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("응답 파싱 실패: %w", err)
	}
	return &rel, nil
}

func extractFromTarGz(r io.Reader, binaryName string) (io.Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := hdr.Name
		if name == binaryName || strings.HasSuffix(name, "/"+binaryName) {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			return bytes.NewReader(data), nil
		}
	}
	return nil, fmt.Errorf("%q 바이너리를 아카이브에서 찾을 수 없습니다", binaryName)
}
