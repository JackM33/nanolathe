package audio

import (
	"fmt"
	"reflect"
	"testing"
)

// Idle clears controller status before its query, and only a playing reply
// runs stop/reset [03 R-AUD-01 §4].
func TestMusicIdleStopPolarityAndQueryOrder(t *testing.T) {
	for _, playing := range []bool{false, true} {
		m := NewMusicController()
		m.Open(3)
		m.Configure(ModeIdle, 0)
		m.status, m.nextTrack, m.fadeStep = StatusPlaying, 3, -1
		polls := 0
		m.SetPlaybackPoll(func() bool {
			polls++
			if m.Status() != StatusIdle {
				t.Fatal("idle query preceded status clear")
			}
			return playing
		})
		m.tickFromMedia()
		if m.Status() != StatusIdle || polls != 1 {
			t.Fatalf("idle status=%v polls=%d", m.Status(), polls)
		}
		wantNext, wantStep := 3, int32(-1)
		if playing {
			wantNext, wantStep = 1, 0
		}
		if m.NextTrack() != wantNext || m.fadeStep != wantStep {
			t.Fatalf("playing=%v next=%d step=%d", playing, m.NextTrack(), m.fadeStep)
		}
		m.tickFromMedia()
		if polls != 1 {
			t.Fatal("already-idle controller queried device")
		}
		m.status, m.nextTrack, m.fadeStep = StatusPlaying, 3, -1
		m.Tick(playing)
		if m.Status() != StatusIdle || m.NextTrack() != wantNext || m.fadeStep != wantStep {
			t.Fatal("explicit tick disagrees with the device-polled idle branch")
		}
	}
}

func TestMusicPlayZeroSetsStatusBeforeTick(t *testing.T) {
	m := NewMusicController()
	m.Open(3)
	m.Configure(ModeSequential, 0)
	m.nextTrack = 1
	m.status = StatusPaused
	m.SetPlaybackPoll(func() bool {
		if m.Status() != StatusPlaying {
			t.Fatal("zero play did not arm playing status before tick")
		}
		return false
	})
	if !m.Play(0) || m.CurTrack() != 2 || m.NextTrack() != 2 || m.Status() != StatusPlaying {
		t.Fatalf("zero play: cur=%d next=%d status=%v", m.CurTrack(), m.NextTrack(), m.Status())
	}
}

// The controller submits next+1 before wrapping the retained next to one
// [03 R-AUD-01 §4]. What a device does with that over-count request is unknown;
// the outcomes below are Nanolathe host policy. A backend that accepts it keeps
// its media and the next admitted completion submits two. A rejection of the
// over-count request plays the wrapped track in the same step without a media
// error. A rejected in-range track keeps the idle/error policy, as does a
// rejected wrapped track.
func TestMusicSequentialSubmissionBeforeWrap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		next      int
		reject    map[int]bool
		requests  []int
		wantNext  int
		wantCur   int
		status    StatusMode
		errTrack  int // 0: no recorded media error
		volumeAdd int
	}{
		{name: "accepted over-count", next: 3, requests: []int{4}, wantNext: 1, wantCur: 4, status: StatusPlaying, volumeAdd: 2},
		{name: "rejected over-count", next: 3, reject: map[int]bool{4: true}, requests: []int{4, 1}, wantNext: 1, wantCur: 1, status: StatusPlaying, volumeAdd: 2},
		{name: "rejected wrapped track", next: 3, reject: map[int]bool{4: true, 1: true}, requests: []int{4, 1}, wantNext: 1, status: StatusIdle, errTrack: 1},
		{name: "rejected in-range track", next: 1, reject: map[int]bool{2: true}, requests: []int{2}, wantNext: 2, status: StatusIdle, errTrack: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMusicController()
			m.Open(3)
			m.Configure(ModeSequential, 0)
			m.SetPlaybackPoll(func() bool { return false })
			m.nextTrack = tc.next
			var requests []int
			m.openTrack = func(track int) (MusicPlayer, error) {
				requests = append(requests, track)
				if m.NextTrack() != track || m.Status() != StatusPlaying {
					t.Fatalf("submission state: next=%d status=%v request=%d", m.NextTrack(), m.Status(), track)
				}
				if tc.reject[track] {
					return nil, fmt.Errorf("authored unavailable track %d", track)
				}
				return nil, nil
			}
			before := m.VolumeApplications()
			m.Tick(false)
			if !reflect.DeepEqual(requests, tc.requests) || m.NextTrack() != tc.wantNext || m.Status() != tc.status {
				t.Fatalf("requests=%v next=%d status=%v", requests, m.NextTrack(), m.Status())
			}
			if tc.status == StatusPlaying && m.CurTrack() != tc.wantCur {
				t.Fatalf("current=%d want %d", m.CurTrack(), tc.wantCur)
			}
			if m.VolumeApplications() != before+tc.volumeAdd {
				t.Fatalf("volume applications=%d want %d", m.VolumeApplications()-before, tc.volumeAdd)
			}
			wantErr := ""
			if tc.errTrack != 0 {
				wantErr = fmt.Sprintf("authored unavailable track %d", tc.errTrack)
			}
			if gotErr := fmt.Sprint(m.mediaError); (m.mediaError == nil) != (wantErr == "") || wantErr != "" && gotErr != wantErr {
				t.Fatalf("media error=%v want %q", m.mediaError, wantErr)
			}
			if tc.name != "accepted over-count" {
				return
			}
			// The accepted request keeps its media until a successful
			// completion; that admitted tick submits the retained next plus one.
			m.NotifySuccessfulCompletion()
			if !reflect.DeepEqual(requests, []int{4, 2}) {
				t.Fatalf("post-wrap requests=%v", requests)
			}
		})
	}
}

// Play All through the production pump: each successful completion advances,
// and the file backend's missing track past the last file wraps to track one
// without a reported error (Nanolathe host policy; the device response to the
// over-count request is unknown [03 R-AUD-01 §4]).
func TestMusicPlayAllContinuesPastLastTrack(t *testing.T) {
	old := GlobalOutput()
	out := &musicProbeOutput{}
	SetGlobalOutput(out)
	t.Cleanup(func() { SetGlobalOutput(old) })
	s := NewService(musicFS(t, "1.wav", "2.wav", "3.wav"))
	// Release the playing track, so its file can be removed on Windows.
	t.Cleanup(s.Close)
	s.ConfigureMusic(false)
	s.Music.Configure(ModeSequential, 0)
	s.Music.SetVolume(32)
	s.StartMusic()
	for step := 0; step < 5; step++ {
		if err := s.ServiceMusic(); err != nil {
			t.Fatalf("pump %d: %v", step, err)
		}
		n := len(out.players)
		if n != step+1 || !out.players[n-1].playing || s.Music.Status() != StatusPlaying {
			t.Fatalf("pump %d: players=%v status=%v", step, out.names, s.Music.Status())
		}
		out.players[n-1].playing, out.players[n-1].completed = false, true
	}
	if want := []string{"1.wav", "2.wav", "3.wav", "1.wav", "2.wav"}; !reflect.DeepEqual(out.names, want) {
		t.Fatalf("played %v, want %v", out.names, want)
	}
}

// Controller status writes must not impersonate a device reply when the host
// runs without a media adapter; that mode still models play/pause/stop/end.
func TestMusicStatusWritesDoNotChangeModeledMedia(t *testing.T) {
	m := NewMusicController()
	m.Open(3)
	m.Configure(ModeSingle, 0)
	m.SetRequestedTrack(2)
	m.Play(2)
	m.Pause(true)
	if !m.Play(0) || m.Status() != StatusPlaying {
		t.Fatal("zero play failed to leave paused controller")
	}
	before := m.VolumeApplications()
	m.NotifyTrackEnd()
	m.Tick(false)
	if m.VolumeApplications() == before {
		t.Fatal("controller status write impersonated same-track playing device")
	}
	m.Configure(ModeIdle, 0)
	m.tickFromMedia()
	if m.NextTrack() != 1 || m.Status() != StatusIdle {
		t.Fatal("idle status clear hid modeled playing device")
	}
}

// The play primitive polls before comparing against retained next, including
// after sequential submission has wrapped that field [03 R-AUD-01 §4].
func TestMusicPlayPollsBeforeComparingRetainedNext(t *testing.T) {
	m := NewMusicController()
	m.Open(3)
	m.Configure(ModeSequential, 0)
	m.nextTrack = 3
	var requests []int
	m.openTrack = func(track int) (MusicPlayer, error) {
		requests = append(requests, track)
		return &musicProbePlayer{playing: true}, nil
	}
	m.Tick(false)
	if m.CurTrack() != 4 || m.NextTrack() != 1 {
		t.Fatal("fixture did not submit then wrap")
	}
	if !m.Play(1) || !reflect.DeepEqual(requests, []int{4}) || m.CurTrack() != 4 {
		t.Fatalf("dedupe ignored retained next: requests=%v current=%d", requests, m.CurTrack())
	}

	p := NewMusicController()
	p.Open(3)
	p.nextTrack = 1
	polls := 0
	p.SetPlaybackPoll(func() bool {
		polls++
		if p.NextTrack() != 1 || p.Status() != StatusPlaying {
			t.Fatal("primitive query ordered after next write or before status write")
		}
		return false
	})
	p.Play(2)
	if polls != 1 {
		t.Fatalf("unequal track skipped query: %d", polls)
	}
}
