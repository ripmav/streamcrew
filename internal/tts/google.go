// SPDX-License-Identifier: MIT

package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// googleURL is the synthesize endpoint of the Text-to-Speech API.
const googleURL = "https://texttospeech.googleapis.com/v1/synthesize"

// google synthesizes the speech with the Text-to-Speech REST API
// (B41, ADR-0020 §3). The key stays in the header, so it never appears
// in the URL of an error (ADR-0020 §2).
func (s *Service) google(ctx context.Context, sp Speech) (string, error) {
	key := s.ports.Env(EnvGoogleKey)
	if key == "" {
		return "", fmt.Errorf("tts: the provider google needs the key in %s", EnvGoogleKey)
	}
	if s.ports.Dialer == nil {
		return "", errors.New("tts: the provider google needs a dialer (net:outbound)")
	}
	// The provider renders with the gain of 1: the volume is applied
	// once, at the sink that plays the file (Review 345-5). The answer
	// is a 16-bit PCM WAV (LINEAR16, 24 kHz mono): the local sink
	// decodes WAV only (Review 345-11).
	body := map[string]any{
		"input": map[string]any{"text": sp.Text},
		"audioConfig": map[string]any{
			"audioEncoding": "LINEAR16",
			"speakingRate":  rateOr(sp.Rate),
			"pitch":         googlePitch(sp.Pitch),
		},
	}
	if sp.Voice != "" {
		body["voice"] = map[string]any{"name": sp.Voice}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("tts: google: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ports.GoogleEndpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("tts: google: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)
	var ans struct {
		AudioContent string `json:"audioContent"`
		Error        any    `json:"error"`
	}
	if err := s.do(req, &ans); err != nil {
		return "", err
	}
	if ans.Error != nil {
		return "", fmt.Errorf("tts: google: %v", ans.Error)
	}
	raw, err := base64.StdEncoding.DecodeString(ans.AudioContent)
	if err != nil {
		return "", fmt.Errorf("tts: google: the answer is no audio: %w", err)
	}
	return s.write(ctx, Google, raw, ".wav")
}

// do runs req and decodes the JSON answer; an answer that is no success
// is an error with its body, truncated.
func (s *Service) do(req *http.Request, v any) error {
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("tts: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := readTruncated(res.Body, 200)
		return fmt.Errorf("tts: %s: the answer is %s: %s", req.URL.Host, res.Status, b)
	}
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return fmt.Errorf("tts: %s: the answer: %w", req.URL.Host, err)
	}
	return nil
}

// googlePitch maps the pitch ratio (1 = normal, like the Web Speech
// API) to the Hz offset of the google API: the ratio range 0 to 2
// covers the API range of -20 to 20 Hz.
func googlePitch(pitch float64) float64 {
	p := (pitch - 1) * 20
	if p < -20 {
		return -20
	}
	if p > 20 {
		return 20
	}
	return p
}

// rateOr returns rate if it is positive, 1 otherwise.
func rateOr(rate float64) float64 {
	if rate > 0 {
		return rate
	}
	return 1
}
