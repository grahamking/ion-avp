package elements

import (
	"fmt"
	"sync"
	"time"

	"github.com/at-wat/ebml-go/mkvcore"
	"github.com/at-wat/ebml-go/webm"

	avp "github.com/pion/ion-avp/pkg"
	log "github.com/pion/ion-log"
)

// WebmSaver Module for saving rtp streams to webm
type WebmSaver struct {
	sync.Mutex
	closed                         bool
	audioWriter, videoWriter       webm.BlockWriteCloser
	audioTimestamp, videoTimestamp uint32
	sampleWriter                   *SampleWriter
	cfg                            WebmSaverConfig
}

// Configure WebmSaver.
// e.g. pass just `Audio: true` to record an audio-only stream.
// Audio: Record the audio track.
// Video: Record the video track.
type WebmSaverConfig struct {
	Audio bool
	Video bool
}

// NewWebmSaver Initialize a new webm saver.
// Pass nil to enable audio and video tracks, the normal case.
func NewWebmSaver(cfg *WebmSaverConfig) *WebmSaver {
	if cfg == nil {
		cfg = &WebmSaverConfig{Audio: true, Video: true}
	}
	return &WebmSaver{
		sampleWriter: NewSampleWriter(),
		cfg:          *cfg,
	}
}

// Write sample to webmsaver
func (s *WebmSaver) Write(sample *avp.Sample) error {
	if sample.Type == avp.TypeVP8 {
		s.pushVP8(sample)
	} else if sample.Type == avp.TypeOpus {
		s.pushOpus(sample)
	}
	return nil
}

// Attach attach a child element
func (s *WebmSaver) Attach(e avp.Element) {
	s.sampleWriter.Attach(e)
}

// Close Close the WebmSaver
func (s *WebmSaver) Close() {
	s.Lock()
	defer s.Unlock()

	if s.closed {
		return
	}

	s.closed = true

	hasWriter := false
	if s.audioWriter != nil {
		if err := s.audioWriter.Close(); err != nil {
			log.Errorf("audio close err: %s", err)
		}
		hasWriter = true
	}
	if s.videoWriter != nil {
		if err := s.videoWriter.Close(); err != nil {
			log.Errorf("video close err: %s", err)
		}
		hasWriter = true
	}
	if !hasWriter {
		s.sampleWriter.Close()
	}
}

func (s *WebmSaver) pushOpus(sample *avp.Sample) {
	if !s.cfg.Audio {
		return
	}
	if s.audioWriter == nil && !s.cfg.Video {
		s.initWriter(0, 0)
	}
	if s.audioWriter != nil {
		if s.audioTimestamp == 0 {
			s.audioTimestamp = sample.Timestamp
		}
		t := (sample.Timestamp - s.audioTimestamp) / 48
		if _, err := s.audioWriter.Write(true, int64(t), sample.Payload.([]byte)); err != nil {
			log.Errorf("audio writer err: %s", err)
		}
	}
}

func (s *WebmSaver) pushVP8(sample *avp.Sample) {
	if !s.cfg.Video {
		return
	}
	payload := sample.Payload.([]byte)
	// Read VP8 header.
	videoKeyframe := (payload[0]&0x1 == 0)

	if videoKeyframe {
		// Keyframe has frame information.
		raw := uint(payload[6]) | uint(payload[7])<<8 | uint(payload[8])<<16 | uint(payload[9])<<24
		width := int(raw & 0x3FFF)
		height := int((raw >> 16) & 0x3FFF)

		if s.videoWriter == nil {
			// Initialize WebM saver using received frame size.
			s.initWriter(width, height)
		}
	}

	if s.videoWriter != nil {
		if s.videoTimestamp == 0 {
			s.videoTimestamp = sample.Timestamp
		}
		t := (sample.Timestamp - s.videoTimestamp) / 90
		if _, err := s.videoWriter.Write(videoKeyframe, int64(t), payload); err != nil {
			log.Errorf("video write err: %s", err)
		}
	}
}

func (s *WebmSaver) initWriter(width, height int) {
	options := []mkvcore.BlockWriterOption{
		mkvcore.WithSegmentInfo(&webm.Info{
			TimecodeScale: webm.DefaultSegmentInfo.TimecodeScale,
			MuxingApp:     webm.DefaultSegmentInfo.MuxingApp,
			WritingApp:    webm.DefaultSegmentInfo.WritingApp,
			DateUTC:       time.Now(),
		}),
		mkvcore.WithSeekHead(true),
	}
	var tracks []webm.TrackEntry
	var audioIdx, videoIdx int
	if s.cfg.Audio {
		tracks = append(tracks, webm.TrackEntry{
			Name:            "Audio",
			TrackNumber:     1,
			TrackUID:        12345,
			CodecID:         "A_OPUS",
			TrackType:       2,
			DefaultDuration: 20000000,
			Audio: &webm.Audio{
				SamplingFrequency: 48000.0,
				Channels:          2,
			},
		})
		audioIdx = 0
	}
	if s.cfg.Video {
		var trackNum uint64
		if s.cfg.Audio {
			trackNum = 2
			videoIdx = 1
		} else {
			trackNum = 1
			videoIdx = 0
		}
		tracks = append(tracks, webm.TrackEntry{
			Name:            "Video",
			TrackNumber:     trackNum,
			TrackUID:        67890,
			CodecID:         "V_VP8",
			TrackType:       1,
			DefaultDuration: 20000000,
			Video: &webm.Video{
				PixelWidth:  uint64(width),
				PixelHeight: uint64(height),
			},
		})
	}
	ws, err := webm.NewSimpleBlockWriter(s.sampleWriter, tracks, options...)
	if err != nil {
		log.Errorf("init writer err: %s", err)
	}
	var msg string
	if s.cfg.Audio {
		s.audioWriter = ws[audioIdx]
		msg = "audio only"
	}
	if s.cfg.Video {
		s.videoWriter = ws[videoIdx]
		msg = fmt.Sprintf("video width=%d, height=%d", width, height)
	}
	log.Infof("WebM saver has started with %s", msg)
}

// SampleWriter for writing samples
type SampleWriter struct {
	Node
}

// NewSampleWriter creates a new sample writer
func NewSampleWriter() *SampleWriter {
	return &SampleWriter{}
}

// Write sample
func (w *SampleWriter) Write(p []byte) (n int, err error) {
	err = w.Node.Write(&avp.Sample{
		Type:    TypeBinary,
		Payload: p,
	})

	if err != nil {
		return 0, err
	}

	return len(p), nil
}

func (w *SampleWriter) Close() error {
	w.Node.Close()
	return nil
}
