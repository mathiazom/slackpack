package seaweedfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

type AssignResponse struct {
	Fid       string `json:"fid"`
	URL       string `json:"url"`
	PublicURL string `json:"publicUrl"`
}

func UploadImageToSeaweedFS(masterURL, imageURL string) (string, error) {
	resp, err := http.Get(masterURL + "/dir/assign")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var assign AssignResponse
	if err := json.NewDecoder(resp.Body).Decode(&assign); err != nil {
		return "", err
	}

	imgResp, err := http.Get(imageURL)
	if err != nil {
		return "", err
	}
	defer imgResp.Body.Close()

	imgData, err := io.ReadAll(imgResp.Body)
	if err != nil {
		return "", err
	}

	// TODO: stream?
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "image")
	if err != nil {
		return "", err
	}
	part.Write(imgData)
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

	client := &http.Client{}
	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		return "", err
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("upload failed with status: %d", uploadResp.StatusCode)
	}

	return assign.Fid, nil
}
