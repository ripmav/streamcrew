// SPDX-License-Identifier: MIT

package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopbackDialer is a dialer without the protection of the server mode,
// for the httptest servers of the tests.
type loopbackDialer struct{}

func (loopbackDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

// testService returns a service with the generated directory under
// tmp, the env of the keys, and the endpoints of the httptest servers.
func testService(t *testing.T, env map[string]string, google, azure string) (*Service, string) {
	t.Helper()
	svc, err := New(Ports{
		GeneratedDir:   filepath.Join(t.TempDir(), "generated"),
		Env:            func(k string) string { return env[k] },
		Dialer:         loopbackDialer{},
		GoogleEndpoint: google,
		AzureBase:      func(string) string { return azure },
		Now:            func() time.Time { return time.Unix(0, 123456789) },
	})
	require.NoError(t, err)
	return svc, svc.ports.GeneratedDir
}

// TestGoogleCoversSynthesizeAndKeyHandling covers B41, ADR-0020 §3:
// the google provider posts the text and the mapping of volume, rate
// and pitch, keeps the key out of the URL, and writes the answer under
// the generated directory; the missing key is a clear error.
func TestGoogleCoversSynthesizeAndKeyHandling(t *testing.T) {
	var gotURL string
	var gotKey string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotKey = r.Header.Get("x-goog-api-key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString([]byte("mp3-bytes")) + `"}`))
	}))
	defer srv.Close()

	svc, dir := testService(t, map[string]string{EnvGoogleKey: "secret-key"}, srv.URL, "")
	path, err := svc.Generate(context.Background(), Google, Speech{Text: "hello", Volume: 50, Rate: 1.5, Pitch: 1.5})
	require.NoError(t, err)
	assert.NotContains(t, gotURL, "secret-key", "the key must not appear in the URL")
	assert.Equal(t, "secret-key", gotKey)
	assert.Equal(t, "hello", body["input"].(map[string]any)["text"])
	cfg := body["audioConfig"].(map[string]any)
	assert.Equal(t, "MP3", cfg["audioEncoding"])
	assert.Equal(t, 1.5, cfg["speakingRate"])
	assert.InDelta(t, 10, cfg["pitch"], 0.01, "the pitch ratio 1.5 is +10 Hz")
	assert.Nil(t, cfg["volumeGainDb"],
		"the sink applies the volume, the provider renders with the gain of 1")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "mp3-bytes", string(raw))
	assert.True(t, strings.HasPrefix(filepath.Base(path), "tts-"))
	assert.True(t, strings.HasSuffix(filepath.Base(path), ".mp3"))
	assert.DirExists(t, dir)

	// The missing key is a clear error.
	none, _ := testService(t, map[string]string{}, srv.URL, "")
	_, err = none.Generate(context.Background(), Google, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvGoogleKey)
}

// TestGoogleCoversTheAPIError covers the answer of the google API that
// is no success: the error carries the body, the key stays out of it.
func TestGoogleCoversTheAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad text"}}`))
	}))
	defer srv.Close()
	svc, _ := testService(t, map[string]string{EnvGoogleKey: "secret-key"}, srv.URL, "")
	_, err := svc.Generate(context.Background(), Google, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad text")
	assert.NotContains(t, err.Error(), "secret-key")
}

// TestAzureCoversSSMLAndKeyHandling covers B41, ADR-0020 §3: the azure
// provider posts the SSML with the voice, the language and the prosody
// mapping, keeps the key out of the URL, and writes the answer; the
// missing key and the missing region are clear errors.
func TestAzureCoversSSMLAndKeyHandling(t *testing.T) {
	var gotKey string
	var gotFormat string
	var gotSSML []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		gotFormat = r.Header.Get("X-Microsoft-OutputFormat")
		gotSSML, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	defer srv.Close()

	svc, dir := testService(t, map[string]string{EnvAzureKey: "secret-key", EnvAzureRegion: "westeurope"}, "", srv.URL)
	path, err := svc.Generate(context.Background(), Azure, Speech{
		Text: "a <b> & \"c\"", Volume: 80, Rate: 1.2, Pitch: 0.9, Voice: "de-DE-KatjaNeural",
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-key", gotKey)
	assert.Equal(t, "audio-24khz-48kbitrate-mono-mp3", gotFormat)
	assert.Contains(t, string(gotSSML), `xml:lang="de-DE"`)
	assert.Contains(t, string(gotSSML), `name="de-DE-KatjaNeural"`)
	assert.Contains(t, string(gotSSML), `rate="+20%"`)
	assert.Contains(t, string(gotSSML), `pitch="-10%"`)
	assert.NotContains(t, string(gotSSML), "volume=",
		"the sink applies the volume, the provider renders with the gain of 1")
	assert.Contains(t, string(gotSSML), "a &lt;b&gt; &amp; &#34;c&#34;")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "mp3-bytes", string(raw))
	assert.True(t, strings.HasSuffix(path, ".mp3"))
	assert.DirExists(t, dir)

	// Without a voice in the action the default voice is used, and it
	// is a voice the API knows (Review 345-6).
	var defaultSSML []byte
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultSSML, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("mp3-bytes"))
	}))
	defer srv2.Close()
	svc2, _ := testService(t, map[string]string{EnvAzureKey: "secret-key", EnvAzureRegion: "westeurope"}, "", srv2.URL)
	_, err = svc2.Generate(context.Background(), Azure, Speech{Text: "hello"})
	require.NoError(t, err)
	assert.Contains(t, string(defaultSSML), `name="en-US-JennyNeural"`)
	assert.Contains(t, string(defaultSSML), `xml:lang="en-US"`)

	// The missing key and the missing region are clear errors.
	noKey, _ := testService(t, map[string]string{EnvAzureRegion: "westeurope"}, "", srv.URL)
	_, err = noKey.Generate(context.Background(), Azure, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvAzureKey)
	noRegion, _ := testService(t, map[string]string{EnvAzureKey: "k"}, "", srv.URL)
	_, err = noRegion.Generate(context.Background(), Azure, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvAzureRegion)
}

// TestAzureSSMLEscapesTheLanguage is the regression of the xml:lang
// (Review 345-7): the language is the voice name without the last
// segment, and it is escaped like the rest of the document, so a voice
// name with XML characters cannot break the SSML.
func TestAzureSSMLEscapesTheLanguage(t *testing.T) {
	ssml := azureSSML("en-US-A&B-CNeural", Speech{Text: "hi"})
	assert.Contains(t, string(ssml), `xml:lang="en-US-A&amp;B"`)
	assert.Contains(t, string(ssml), `name="en-US-A&amp;B-CNeural"`)
}

// TestPiperCoversTheProcessAndModel covers B41, ADR-0020 §3: the piper
// provider runs the process with the model, the output file and the
// mapping of rate and volume, and returns the written file; the missing
// model is a clear error.
func TestPiperCoversTheProcessAndModel(t *testing.T) {
	var args []string
	var text string
	svc, _ := testService(t, map[string]string{}, "", "")
	svc.ports.PiperModel = "/models/de.pth"
	svc.ports.Piper = func(_ context.Context, a []string, in string) error {
		args = a
		text = in
		for i := 1; i+1 < len(a); i++ {
			if a[i] == "--output_file" {
				require.NoError(t, os.WriteFile(a[i+1], []byte("wav-bytes"), 0o600))
			}
		}
		return nil
	}
	path, err := svc.Generate(context.Background(), Piper, Speech{Text: "hallo", Rate: 2, Volume: 50})
	require.NoError(t, err)
	assert.Equal(t, "hallo", text)
	assert.Equal(t, []string{
		"--model", "/models/de.pth",
		"--output_file", path,
		"--length-scale", "0.5000",
	}, args)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "wav-bytes", string(raw))

	// The missing model is a clear error.
	none, _ := testService(t, map[string]string{}, "", "")
	none.ports.Piper = svc.ports.Piper
	_, err = none.Generate(context.Background(), Piper, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tts-piper-model")
}

// TestGenerateCoversTheProviderNames covers Generate: the browser
// provider generates no file, and the unknown provider is a clear
// error.
func TestGenerateCoversTheProviderNames(t *testing.T) {
	svc, _ := testService(t, map[string]string{}, "", "")
	_, err := svc.Generate(context.Background(), Browser, Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlay")
	_, err = svc.Generate(context.Background(), Name("nope"), Speech{Text: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

// TestNewCoversTheGeneratedDir covers New: the service needs the
// generated directory.
func TestNewCoversTheGeneratedDir(t *testing.T) {
	_, err := New(Ports{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generated")
}
