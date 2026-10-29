// SPDX-License-Identifier: MIT

package tts

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// defaultAzureVoice is the voice of the azure provider when the action
// names none; the region of the endpoint still comes from
// EnvAzureRegion.
const defaultAzureVoice = "en-US-Neural"

// azureBase is the production endpoint of the Speech API for the region.
func azureBase(region string) string {
	return "https://" + region + ".tts.speech.microsoft.com/cognitiveservices/v1"
}

// azure synthesizes the speech with the Speech REST API (B41,
// ADR-0020 §3). The key stays in the header, so it never appears in the
// URL of an error (ADR-0020 §2).
func (s *Service) azure(ctx context.Context, sp Speech) (string, error) {
	key := s.ports.Env(EnvAzureKey)
	if key == "" {
		return "", fmt.Errorf("tts: the provider azure needs the key in %s", EnvAzureKey)
	}
	region := s.ports.Env(EnvAzureRegion)
	if region == "" {
		return "", fmt.Errorf("tts: the provider azure needs the region in %s", EnvAzureRegion)
	}
	if s.ports.Dialer == nil {
		return "", errors.New("tts: the provider azure needs a dialer (net:outbound)")
	}
	voice := sp.Voice
	if voice == "" {
		voice = defaultAzureVoice
	}
	body := azureSSML(voice, sp)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.ports.AzureBase(region), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("tts: azure: %w", err)
	}
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("Ocp-Apim-Subscription-Key", key)
	req.Header.Set("X-Microsoft-OutputFormat", "audio-24khz-48kbitrate-mono-mp3")
	res, err := s.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("tts: azure: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := readTruncated(res.Body, 200)
		return "", fmt.Errorf("tts: azure: the answer is %s: %s", res.Status, b)
	}
	raw, err := readTruncated(res.Body, 32<<20)
	if err != nil {
		return "", fmt.Errorf("tts: azure: the answer: %w", err)
	}
	return s.write(ctx, Azure, raw, ".mp3")
}

// azureSSML builds the SSML document of the request: the prosody maps
// the volume 0 to 100, the rate and the pitch ratio 1 = normal to the
// relative values of the API (±%).
func azureSSML(voice string, sp Speech) []byte {
	lang := voice
	if i := strings.LastIndex(lang, "-"); i > 0 {
		lang = lang[:i]
	}
	var b bytes.Buffer
	b.WriteString(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="` + lang + `">`)
	b.WriteString(`<voice name="` + xmlEscape(voice) + `">`)
	b.WriteString(`<prosody rate="` + percent((sp.Rate-1)*100) +
		`" pitch="` + percent((sp.Pitch-1)*100) +
		`" volume="` + percent((sp.Volume/100-1)*100) +
		`">`)
	b.WriteString(xmlEscape(sp.Text))
	b.WriteString(`</prosody></voice></speak>`)
	return b.Bytes()
}

// percent formats v as the ± percent value of the SSML prosody; the
// float arithmetic of the ratio is rounded, so 1.2 is +20%.
func percent(v float64) string {
	n := max(min(int(math.Round(v)), 100), -100)
	return fmt.Sprintf("%+d%%", n)
}

// xmlEscape escapes the five characters of the XML text.
func xmlEscape(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// EscapeText fails only on a non-writable buffer.
		return s
	}
	return b.String()
}
