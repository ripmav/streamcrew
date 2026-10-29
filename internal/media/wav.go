// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// targetRate and targetChannels are the parameters the local sink
// normalizes to before it plays (ADR-0020 §1): the audio backend allows
// one sample rate and one channel count per process.
const (
	targetRate     = 44100
	targetChannels = 2
)

// errNotWAV is the error of a file that is no 16-bit PCM WAV.
//
// shortcut: the local sink decodes 16-bit PCM WAV only (ADR-0020 §1: WAV
// is trivially decodable; the MP3 candidate of the ADR, dmarc/ito/mp3, is
// gone as of 7.3, so MP3 stays a clear error). Upgrade: float32 and
// 24-bit PCM, and an MP3 decoder.
var errNotWAV = errors.New("media: the file is no 16-bit PCM WAV")

// decodeWAV decodes a 16-bit PCM WAV file, mono or stereo, at any sample
// rate, and returns the interleaved samples, the sample rate in Hz and
// the number of channels.
func decodeWAV(data []byte) (samples []int16, rate, channels int, err error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, errNotWAV
	}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if size < 0 || pos+8+size > len(data) {
			return nil, 0, 0, fmt.Errorf("%w: a chunk is truncated", errNotWAV)
		}
		payload := data[pos+8 : pos+8+size]
		pos += 8 + size
		// Chunks are padded to an even size.
		if size%2 == 1 {
			pos++
		}
		switch id {
		case "fmt ":
			rate, channels, err = parseWAVFormat(payload)
			if err != nil {
				return nil, 0, 0, err
			}
		case "data":
			if len(payload)%2 != 0 {
				return nil, 0, 0, fmt.Errorf("%w: the data chunk is truncated", errNotWAV)
			}
			samples = make([]int16, len(payload)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(payload[2*i:])) //nolint:gosec // the two's-complement sample, high bit negative
			}
		}
	}
	if rate == 0 || channels == 0 {
		return nil, 0, 0, fmt.Errorf("%w: no fmt chunk", errNotWAV)
	}
	if samples == nil {
		return nil, 0, 0, fmt.Errorf("%w: no data chunk", errNotWAV)
	}
	return samples, rate, channels, nil
}

// parseWAVFormat decodes the fmt chunk (16-bit PCM only, ADR-0020 §1).
func parseWAVFormat(data []byte) (rate, channels int, err error) {
	if len(data) < 16 {
		return 0, 0, fmt.Errorf("%w: the fmt chunk is truncated", errNotWAV)
	}
	format := binary.LittleEndian.Uint16(data)
	channels = int(binary.LittleEndian.Uint16(data[2:]))
	rate = int(binary.LittleEndian.Uint32(data[4:]))
	bits := binary.LittleEndian.Uint16(data[14:])
	switch {
	case format != 1:
		return 0, 0, fmt.Errorf("%w: the audio format %d is not PCM", errNotWAV, format)
	case channels < 1 || channels > 2:
		return 0, 0, fmt.Errorf("%w: the %d channels are not supported", errNotWAV, channels)
	case bits != 16:
		return 0, 0, fmt.Errorf("%w: the %d-bit depth is not supported", errNotWAV, bits)
	default:
		return rate, channels, nil
	}
}

// normalize converts the interleaved 16-bit samples to the format of
// the context: float32 little endian, mono to stereo, and a linear
// resample to targetRate.
func normalize(samples []int16, rate, channels int) []byte {
	f := make([]float32, len(samples))
	for i, v := range samples {
		f[i] = float32(v) / 32768.0
	}
	if channels == 1 {
		st := make([]float32, len(f)*2)
		for i, v := range f {
			st[2*i], st[2*i+1] = v, v
		}
		f = st
	}
	if rate != targetRate {
		out := make([]float32, int(int64(len(f))*targetRate/int64(rate)))
		ratio := float64(rate) / float64(targetRate)
		for i := range out {
			pos := float64(i) * ratio
			j := int(pos)
			frac := float32(pos - float64(j))
			b := f[j]
			if j+1 < len(f) {
				b = f[j+1]
			}
			out[i] = f[j]*(1-frac) + b*frac
		}
		f = out
	}
	var buf bytes.Buffer
	buf.Grow(len(f) * 4)
	for _, v := range f {
		var tmp [4]byte
		binary.LittleEndian.PutUint32(tmp[:], math.Float32bits(v))
		buf.Write(tmp[:])
	}
	return buf.Bytes()
}
