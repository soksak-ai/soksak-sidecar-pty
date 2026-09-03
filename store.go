package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// A session's record outlives the process that wrote it. The name states the format version, so a
// record in an older shape is not found rather than found and refused, and no reader for an older
// shape can exist to be written. A format change is a new prefix here and the removal of this one.
const recordVersion = "v1"

// storeDirName is where a home keeps its sessions. It is under the home rather than the runtime
// root: the runtime root holds sockets and a token, which a boot may recreate, and a record has to
// survive a boot.
const storeDirName = "pty-sessions"

// outputSegmentBound is how much output one segment holds. Two segments are kept, so a session
// retains between one and two of these, and a restore replays that at the terminal contract's
// 80 MB/s floor: four mebibytes is fifty milliseconds, eight is a hundred.
//
// Two segments rather than one file trimmed in place. Trimming copies the retained bytes on every
// pass; alternating segments drops the older one whole and copies nothing.
const outputSegmentBound = 4 << 20

// sessionRecord is what one session leaves on disk. It holds the facts an equivalent session is
// created from and the mark that states how the owner ended.
type sessionRecord struct {
	Session         uint64   `json:"session"`
	PaneID          string   `json:"paneId"`
	WindowLabel     string   `json:"windowLabel"`
	CWD             string   `json:"cwd"`
	Command         string   `json:"command"`
	Environment     []string `json:"environment"`
	Cols            uint16   `json:"cols"`
	Rows            uint16   `json:"rows"`
	StartedAtUnixMs int64    `json:"startedAtUnixMs"`
	// EndedAtUnixMs is set by the stop write and by nothing else. Its absence is the only evidence
	// that the owner ended without warning.
	EndedAtUnixMs *int64 `json:"endedAtUnixMs,omitempty"`
	ExitCode      *int64 `json:"exitCode,omitempty"`
	// Segment is which output file is being appended to. A reader takes the other one first.
	Segment int `json:"segment"`
	// Modes is the mode state a replay cannot rebuild, as the component that parses reported it.
	// This daemon parses no output, so it stores this opaquely and reads nothing out of it. Empty
	// until something reports one, and a restore from an empty one applies nothing rather than a
	// guess.
	Modes []byte `json:"modes,omitempty"`
}

type store struct {
	dir string

	// mu guards the writer map and nothing else. A file write happens under the writer's own lock,
	// so one session appending never pauses another: sixteen shells producing output would
	// otherwise queue on one lock through sixteen disk writes.
	mu   sync.Mutex
	open map[uint64]*segmentWriter
}

// segmentWriter appends to one session's current segment and rolls to the other at the bound.
type segmentWriter struct {
	mu      sync.Mutex
	file    *os.File
	segment int
	written int
}

func newStore(home string) (*store, error) {
	dir := filepath.Join(home, storeDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("preparing the session store: %w", err)
	}
	return &store{dir: dir, open: make(map[uint64]*segmentWriter)}, nil
}

func (s *store) recordPath(id uint64) string {
	return filepath.Join(s.dir, recordVersion+"-"+strconv.FormatUint(id, 10)+".json")
}

func (s *store) segmentPath(id uint64, segment int) string {
	return filepath.Join(s.dir,
		recordVersion+"-"+strconv.FormatUint(id, 10)+".out"+strconv.Itoa(segment))
}

// create writes the facts an equivalent session is made from. It leaves the record unmarked: only a
// stop write marks one, and everything a crash preserves starts here.
func (s *store) create(record sessionRecord) error {
	return s.write(record)
}

// write replaces the record atomically. A reader never sees a partial one.
func (s *store) write(record sessionRecord) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	target := s.recordPath(record.Session)
	temporary := target + ".next"
	if err := os.WriteFile(temporary, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}

// read returns one session's record. A record whose stated id does not match the path it was found
// at is refused rather than repaired: one of the two is wrong and neither says which.
func (s *store) read(id uint64) (sessionRecord, error) {
	var record sessionRecord
	body, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return record, fmt.Errorf("record %d does not parse: %w", id, err)
	}
	if record.Session != id {
		return record, fmt.Errorf("the record at %d states session %d", id, record.Session)
	}
	return record, nil
}

// markEnded is the stop write. It states that this owner ended on purpose.
func (s *store) markEnded(id uint64, at int64, exitCode *int64) error {
	record, err := s.read(id)
	if err != nil {
		return err
	}
	if writer := s.heldWriter(id); writer != nil {
		writer.mu.Lock()
		if writer.file != nil {
			record.Segment = writer.segment
			// The stop write forces the platter. A stop is the point a power cycle recovers from,
			// and page cache does not survive one.
			_ = writer.file.Sync()
		}
		writer.mu.Unlock()
	}
	record.EndedAtUnixMs = &at
	record.ExitCode = exitCode
	return s.write(record)
}

// writerFor returns this session's writer, opening it on first use. Only the map is guarded here.
func (s *store) writerFor(id uint64) *segmentWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	writer, held := s.open[id]
	if !held {
		writer = &segmentWriter{segment: -1}
		s.open[id] = writer
	}
	return writer
}

// setModes records the mode state for one session. Modes change when a program enters or leaves a
// full-screen mode, which is rare, so this is written on change rather than on a cadence.
func (s *store) setModes(id uint64, report []byte) error {
	record, err := s.read(id)
	if err != nil {
		return err
	}
	record.Modes = append([]byte(nil), report...)
	return s.write(record)
}

// append adds output to the session's current segment. The write goes as far as the operating
// system and is not forced to the platter: that is what a process exit needs, and a process exit is
// what a restore recovers from.
func (s *store) append(id uint64, data []byte) error {
	writer := s.writerFor(id)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.file == nil {
		record, err := s.read(id)
		if err != nil {
			return err
		}
		if err := s.openInto(writer, id, record.Segment); err != nil {
			return err
		}
	}
	if writer.written+len(data) > outputSegmentBound && writer.written > 0 {
		if err := s.roll(id, writer); err != nil {
			return err
		}
	}
	count, err := writer.file.Write(data)
	writer.written += count
	return err
}

// roll moves to the other segment and drops what was there. The dropped segment is the older half
// of the retained output, which is what the bound gives up. The caller holds the writer's lock.
func (s *store) roll(id uint64, writer *segmentWriter) error {
	next := 1 - writer.segment
	_ = writer.file.Close()
	writer.file = nil
	if err := os.Remove(s.segmentPath(id, next)); err != nil && !os.IsNotExist(err) {
		return err
	}
	record, err := s.read(id)
	if err != nil {
		return err
	}
	record.Segment = next
	if err := s.write(record); err != nil {
		return err
	}
	return s.openInto(writer, id, next)
}

// openInto opens one segment into the writer. The caller holds the writer's lock.
func (s *store) openInto(writer *segmentWriter, id uint64, segment int) error {
	if segment != 0 && segment != 1 {
		segment = 0
	}
	file, err := os.OpenFile(s.segmentPath(id, segment), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	writer.file, writer.segment, writer.written = file, segment, int(info.Size())
	return nil
}

// output returns the retained output oldest first. The record names the segment being appended to,
// so the other one holds what came before it.
func (s *store) output(id uint64) ([]byte, error) {
	record, err := s.read(id)
	if err != nil {
		return nil, err
	}
	if writer := s.heldWriter(id); writer != nil {
		writer.mu.Lock()
		if writer.file != nil {
			record.Segment = writer.segment
		}
		writer.mu.Unlock()
	}
	older, err := os.ReadFile(s.segmentPath(id, 1-record.Segment))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	newer, err := os.ReadFile(s.segmentPath(id, record.Segment))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return append(older, newer...), nil
}

// list returns every session this store holds a record for, in this format version.
func (s *store) list() ([]uint64, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, recordVersion+"-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		digits := strings.TrimSuffix(strings.TrimPrefix(name, recordVersion+"-"), ".json")
		id, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// remove drops a session's record and its output.
func (s *store) remove(id uint64) error {
	s.mu.Lock()
	writer, held := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()
	if held {
		writer.mu.Lock()
		if writer.file != nil {
			_ = writer.file.Close()
			writer.file = nil
		}
		writer.mu.Unlock()
	}
	first := os.Remove(s.recordPath(id))
	for segment := 0; segment < 2; segment++ {
		if err := os.Remove(s.segmentPath(id, segment)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if first != nil && !os.IsNotExist(first) {
		return first
	}
	return nil
}

// sweep removes every record no session in the index names. Nothing removes a record on its own, so
// an owner that never swept would grow its store by every session that ever ran.
func (s *store) sweep(named map[uint64]bool) (int, error) {
	ids, err := s.list()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		if named[id] {
			continue
		}
		if err := s.remove(id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// heldWriter returns the writer this session already has, or nil.
func (s *store) heldWriter(id uint64) *segmentWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[id]
}

func (s *store) close() error {
	s.mu.Lock()
	writers := make([]*segmentWriter, 0, len(s.open))
	for id, writer := range s.open {
		writers = append(writers, writer)
		delete(s.open, id)
	}
	s.mu.Unlock()
	for _, writer := range writers {
		writer.mu.Lock()
		if writer.file != nil {
			_ = writer.file.Close()
			writer.file = nil
		}
		writer.mu.Unlock()
	}
	return nil
}
