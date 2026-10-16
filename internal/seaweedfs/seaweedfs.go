package seaweedfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

type AssignResponse struct {
	Fid       string `json:"fid"`
	URL       string `json:"url"`
	PublicURL string `json:"publicUrl"`
}

func UploadToSeaweedFS(masterURL string, data []byte, filename string) (string, error) {
	resp, err := http.Get(masterURL + "/dir/assign")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var assign AssignResponse
	if err := json.NewDecoder(resp.Body).Decode(&assign); err != nil {
		return "", err
	}

	// TODO: stream?
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	part.Write(data)
	writer.Close()

	// TODO: https?
	uploadReq, err := http.NewRequest("POST", "http://"+assign.URL+"/"+assign.Fid, &buf)
	if err != nil {
		return "", err
	}
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	jwt := resp.Header.Get("Authorization")
	if jwt != "" {
		uploadReq.Header.Set("Authorization", jwt)
	}

	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		return "", err
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("upload failed with status: %d", uploadResp.StatusCode)
	}

	return assign.Fid, nil
}

type lookupResponse struct {
	Locations []struct {
		URL string `json:"url"`
	} `json:"locations"`
}

func DownloadFromSeaweedFS(masterURL string, fid string) ([]byte, error) {
	volumeId, _, found := strings.Cut(fid, ",")
	if !found {
		return nil, fmt.Errorf("invalid fid: %s", fid)
	}

	resp, err := http.Get(masterURL + "/dir/lookup?volumeId=" + url.QueryEscape(volumeId))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var lookup lookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&lookup); err != nil {
		return nil, err
	}
	if len(lookup.Locations) == 0 {
		return nil, fmt.Errorf("no locations for volume %s", volumeId)
	}

	// TODO: https?
	fileResp, err := http.Get("http://" + lookup.Locations[0].URL + "/" + fid)
	if err != nil {
		return nil, err
	}
	defer fileResp.Body.Close()

	if fileResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", fileResp.StatusCode)
	}

	return io.ReadAll(fileResp.Body)
}
