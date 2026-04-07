package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultTranscriptionModel = "gpt-4o-mini-transcribe"

func maybeTranscribeUpload(absPath, contentType string) (string, error) {
	if !isAudioUpload(absPath, contentType) {
		return "", nil
	}

	return transcribeAudioFile(absPath)
}

func transcribeAudioFile(path string) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY is not set")
	}

	reqBody, contentType, err := buildTranscriptionRequest(path, transcriptionModel())
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/transcriptions", reqBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.New("transcription timed out")
		}
		return "", err
	}
	defer resp.Body.Close()

	return parseTranscriptionResponse(resp)
}

func transcriptionModel() string {
	model := strings.TrimSpace(os.Getenv("TCLAW_TRANSCRIBE_MODEL"))
	if model == "" {
		return defaultTranscriptionModel
	}
	return model
}

func buildTranscriptionRequest(path, model string) (*bytes.Buffer, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("model", model); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	return &body, writer.FormDataContentType(), nil
}

func parseTranscriptionResponse(resp *http.Response) (string, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = "transcription request failed"
		}
		return "", errors.New(msg)
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", err
	}

	return strings.TrimSpace(payload.Text), nil
}
