package composer

import (
	"encoding/binary"
	"io"
	"strings"
)

const (
	maximumMP4BoxDepth     = 8
	maximumMP4BoxCount     = 4096
	maximumMP4LeafBoxBytes = 4096
)

type mp4Metadata struct {
	Width               int
	Height              int
	DurationSeconds     float64
	VideoCodec          string
	AudioCodec          string
	HasAudio            bool
	MoovBeforeMediaData bool
}

type mp4TrackMetadata struct {
	handler         string
	width           int
	height          int
	durationSeconds float64
	codec           string
}

type mp4InspectState struct {
	boxCount       int
	foundMoov      bool
	foundMediaData bool
	movieDuration  float64
	video          mp4TrackMetadata
	audio          mp4TrackMetadata
}

type mp4Box struct {
	typ         string
	size        int64
	payloadSize int64
}

func inspectMP4Metadata(reader io.Reader, totalSize int64) (mp4Metadata, error) {
	if totalSize < 24 {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	state := &mp4InspectState{}
	first := true
	var metadata mp4Metadata
	if err := parseMP4Boxes(reader, totalSize, 0, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		if first && box.typ != "ftyp" {
			return invalidInspectedMedia("media_video_invalid")
		}
		first = false
		switch box.typ {
		case "moov":
			state.foundMoov = true
			metadata.MoovBeforeMediaData = !state.foundMediaData
			return parseMP4Moov(payload, box.payloadSize, 1, state)
		case "mdat":
			state.foundMediaData = true
		}
		return nil
	}); err != nil {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	if !state.foundMoov || !state.foundMediaData {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	if state.video.width < 1 || state.video.height < 1 || state.video.codec == "" {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	metadata.Width = state.video.width
	metadata.Height = state.video.height
	metadata.VideoCodec = state.video.codec
	metadata.AudioCodec = state.audio.codec
	metadata.HasAudio = state.audio.handler == "soun"
	if metadata.HasAudio && metadata.AudioCodec == "" {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	metadata.DurationSeconds = maximumFloat64(
		state.video.durationSeconds,
		state.audio.durationSeconds,
		state.movieDuration,
	)
	if metadata.DurationSeconds <= 0 {
		return mp4Metadata{}, invalidInspectedMedia("media_video_invalid")
	}
	return metadata, nil
}

func parseMP4Moov(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
) error {
	return parseMP4Boxes(reader, remaining, depth, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		switch box.typ {
		case "mvhd":
			duration, err := parseMP4MovieHeader(payload, box.payloadSize)
			if err != nil {
				return err
			}
			state.movieDuration = duration
		case "trak":
			track, err := parseMP4Track(payload, box.payloadSize, depth+1, state)
			if err != nil {
				return err
			}
			switch track.handler {
			case "vide":
				if state.video.handler == "" {
					state.video = track
				}
			case "soun":
				if state.audio.handler == "" {
					state.audio = track
				}
			}
		}
		return nil
	})
}

func parseMP4Track(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
) (mp4TrackMetadata, error) {
	track := mp4TrackMetadata{}
	err := parseMP4Boxes(reader, remaining, depth, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		switch box.typ {
		case "tkhd":
			width, height, err := parseMP4TrackHeader(payload, box.payloadSize)
			if err != nil {
				return err
			}
			track.width = width
			track.height = height
		case "mdia":
			return parseMP4Media(payload, box.payloadSize, depth+1, state, &track)
		}
		return nil
	})
	return track, err
}

func parseMP4Media(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
	track *mp4TrackMetadata,
) error {
	return parseMP4Boxes(reader, remaining, depth, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		switch box.typ {
		case "mdhd":
			duration, err := parseMP4MediaHeader(payload, box.payloadSize)
			if err != nil {
				return err
			}
			track.durationSeconds = duration
		case "hdlr":
			handler, err := parseMP4Handler(payload, box.payloadSize)
			if err != nil {
				return err
			}
			track.handler = handler
		case "minf":
			return parseMP4MediaInfo(payload, box.payloadSize, depth+1, state, track)
		}
		return nil
	})
}

func parseMP4MediaInfo(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
	track *mp4TrackMetadata,
) error {
	return parseMP4Boxes(reader, remaining, depth, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		if box.typ == "stbl" {
			return parseMP4SampleTable(payload, box.payloadSize, depth+1, state, track)
		}
		return nil
	})
}

func parseMP4SampleTable(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
	track *mp4TrackMetadata,
) error {
	return parseMP4Boxes(reader, remaining, depth, state, func(
		box mp4Box,
		payload io.Reader,
	) error {
		if box.typ != "stsd" {
			return nil
		}
		codec, err := parseMP4SampleDescription(payload, box.payloadSize, track.handler)
		if err != nil {
			return err
		}
		track.codec = codec
		return nil
	})
}

func parseMP4Boxes(
	reader io.Reader,
	remaining int64,
	depth int,
	state *mp4InspectState,
	visit func(mp4Box, io.Reader) error,
) error {
	if depth > maximumMP4BoxDepth {
		return invalidInspectedMedia("media_video_invalid")
	}
	header := make([]byte, 16)
	for remaining > 0 {
		state.boxCount++
		if state.boxCount > maximumMP4BoxCount {
			return invalidInspectedMedia("media_video_invalid")
		}
		if remaining < 8 {
			return invalidInspectedMedia("media_video_invalid")
		}
		if _, err := io.ReadFull(reader, header[:8]); err != nil {
			return err
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		boxType := string(header[4:8])
		headerSize := int64(8)
		if boxSize == 1 {
			if remaining < 16 {
				return invalidInspectedMedia("media_video_invalid")
			}
			if _, err := io.ReadFull(reader, header[8:16]); err != nil {
				return err
			}
			extendedSize := binary.BigEndian.Uint64(header[8:16])
			if extendedSize > uint64(remaining) {
				return invalidInspectedMedia("media_video_invalid")
			}
			boxSize = int64(extendedSize)
			headerSize = 16
		} else if boxSize == 0 {
			boxSize = remaining
		}
		if boxSize < headerSize || boxSize > remaining {
			return invalidInspectedMedia("media_video_invalid")
		}
		payloadSize := boxSize - headerSize
		limited := &io.LimitedReader{R: reader, N: payloadSize}
		if err := visit(mp4Box{
			typ:         boxType,
			size:        boxSize,
			payloadSize: payloadSize,
		}, limited); err != nil {
			return err
		}
		if _, err := io.CopyN(io.Discard, limited, limited.N); err != nil {
			return err
		}
		remaining -= boxSize
	}
	return nil
}

func parseMP4MovieHeader(reader io.Reader, size int64) (float64, error) {
	payload, err := readMP4Leaf(reader, size)
	if err != nil {
		return 0, err
	}
	return mp4DurationFromHeader(payload)
}

func parseMP4MediaHeader(reader io.Reader, size int64) (float64, error) {
	payload, err := readMP4Leaf(reader, size)
	if err != nil {
		return 0, err
	}
	return mp4DurationFromHeader(payload)
}

func mp4DurationFromHeader(payload []byte) (float64, error) {
	if len(payload) < 20 {
		return 0, invalidInspectedMedia("media_video_invalid")
	}
	version := payload[0]
	switch version {
	case 0:
		timescale := binary.BigEndian.Uint32(payload[12:16])
		duration := binary.BigEndian.Uint32(payload[16:20])
		if timescale == 0 {
			return 0, invalidInspectedMedia("media_video_invalid")
		}
		return float64(duration) / float64(timescale), nil
	case 1:
		if len(payload) < 32 {
			return 0, invalidInspectedMedia("media_video_invalid")
		}
		timescale := binary.BigEndian.Uint32(payload[20:24])
		duration := binary.BigEndian.Uint64(payload[24:32])
		if timescale == 0 {
			return 0, invalidInspectedMedia("media_video_invalid")
		}
		return float64(duration) / float64(timescale), nil
	default:
		return 0, invalidInspectedMedia("media_video_invalid")
	}
}

func parseMP4TrackHeader(reader io.Reader, size int64) (int, int, error) {
	payload, err := readMP4Leaf(reader, size)
	if err != nil {
		return 0, 0, err
	}
	if len(payload) < 8 {
		return 0, 0, invalidInspectedMedia("media_video_invalid")
	}
	width := int(binary.BigEndian.Uint32(payload[len(payload)-8:len(payload)-4]) >> 16)
	height := int(binary.BigEndian.Uint32(payload[len(payload)-4:]) >> 16)
	return width, height, nil
}

func parseMP4Handler(reader io.Reader, size int64) (string, error) {
	payload, err := readMP4Leaf(reader, size)
	if err != nil {
		return "", err
	}
	if len(payload) < 12 {
		return "", invalidInspectedMedia("media_video_invalid")
	}
	return string(payload[8:12]), nil
}

func parseMP4SampleDescription(
	reader io.Reader,
	size int64,
	handler string,
) (string, error) {
	payload, err := readMP4Leaf(reader, size)
	if err != nil {
		return "", err
	}
	if len(payload) < 16 {
		return "", invalidInspectedMedia("media_video_invalid")
	}
	entryCount := binary.BigEndian.Uint32(payload[4:8])
	if entryCount == 0 {
		return "", invalidInspectedMedia("media_video_invalid")
	}
	entrySize := binary.BigEndian.Uint32(payload[8:12])
	if entrySize < 8 || int(entrySize) > len(payload)-8 {
		return "", invalidInspectedMedia("media_video_invalid")
	}
	return canonicalizeMP4Codec(handler, string(payload[12:16])), nil
}

func readMP4Leaf(reader io.Reader, size int64) ([]byte, error) {
	if size < 1 || size > maximumMP4LeafBoxBytes {
		return nil, invalidInspectedMedia("media_video_invalid")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func canonicalizeMP4Codec(handler, codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "avc1", "avc3":
		return "h264"
	case "hev1", "hvc1":
		return "hevc"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "mp4v":
		return "mpeg4"
	case "mp4a":
		if handler == "soun" {
			return "aac"
		}
	case "ac-3":
		return "ac3"
	case "ec-3":
		return "eac3"
	case "opus":
		return "opus"
	}
	return strings.ToLower(strings.TrimSpace(codec))
}

func maximumFloat64(values ...float64) float64 {
	maximum := 0.0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
