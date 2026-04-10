package core

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"lumenvec/internal/index"
)

const (
	fileVectorStoreDataFile               = "vectors.dat"
	fileVectorStoreOpPut                  = byte(1)
	fileVectorStoreOpDelete               = byte(2)
	fileVectorStoreCompactMinStaleRecords = 1
	fileVectorStoreCompactStaleDivisor    = 2
)

type fileVectorStore struct {
	basePath string
	dataPath string

	mu       sync.RWMutex
	file     *os.File
	offsets  map[string]fileVectorRecordMeta
	writes   int64
	stale    int64
	compacts int64
}

type fileVectorRecordMeta struct {
	recordOffset int64
	recordLength uint32
	deleted      bool
}

func newFileVectorStore(basePath string) *fileVectorStore {
	store := &fileVectorStore{
		basePath: basePath,
		dataPath: filepath.Join(basePath, fileVectorStoreDataFile),
		offsets:  make(map[string]fileVectorRecordMeta),
	}
	if err := store.open(); err != nil {
		panic(err)
	}
	return store
}

func (s *fileVectorStore) UpsertVector(vec index.Vector) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpenLocked(); err != nil {
		return err
	}
	record, err := encodeFileVectorRecord(fileVectorStoreOpPut, vec.ID, vec.Values)
	if err != nil {
		return err
	}
	offset, err := s.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(record); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	if _, exists := s.offsets[vec.ID]; exists {
		s.stale++
	}
	s.offsets[vec.ID] = fileVectorRecordMeta{recordOffset: offset, recordLength: uint32(len(record))}
	s.writes++
	return s.maybeCompactLocked()
}

func (s *fileVectorStore) GetVector(id string) (index.Vector, error) {
	vec, err := s.GetVectorReadOnly(id)
	if err != nil {
		return index.Vector{}, err
	}
	return index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)}, nil
}

func (s *fileVectorStore) GetVectorReadOnly(id string) (index.Vector, error) {
	s.mu.RLock()
	meta, ok := s.offsets[id]
	file := s.file
	s.mu.RUnlock()

	if !ok || meta.deleted {
		return index.Vector{}, index.ErrVectorNotFound
	}
	if file == nil {
		return index.Vector{}, os.ErrClosed
	}

	record, err := readFileVectorRecordAt(file, meta)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return index.Vector{}, index.ErrVectorNotFound
		}
		return index.Vector{}, err
	}
	if record.op == fileVectorStoreOpDelete {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return index.Vector{ID: record.id, Values: record.values}, nil
}

func (s *fileVectorStore) DeleteVector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, ok := s.offsets[id]
	if !ok || meta.deleted {
		return index.ErrVectorNotFound
	}
	if err := s.ensureOpenLocked(); err != nil {
		return err
	}
	record, err := encodeFileVectorRecord(fileVectorStoreOpDelete, id, nil)
	if err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := s.file.Write(record); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	delete(s.offsets, id)
	s.writes++
	s.stale++
	return s.maybeCompactLocked()
}

func (s *fileVectorStore) ListVectors() []index.Vector {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.file == nil {
		return nil
	}

	out := make([]index.Vector, 0, len(s.offsets))
	for id, meta := range s.offsets {
		if meta.deleted {
			continue
		}
		record, err := readFileVectorRecordAt(s.file, meta)
		if err != nil || record.op == fileVectorStoreOpDelete {
			continue
		}
		out = append(out, index.Vector{ID: id, Values: cloneVectorValues(record.values)})
	}
	return out
}

func (s *fileVectorStore) IsPersistent() bool {
	return true
}

func (s *fileVectorStore) DiskStats() DiskStoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := DiskStoreStats{
		Records:      uint64(len(s.offsets)),
		StaleRecords: uint64(maxInt64(s.stale, 0)),
		Compactions:  uint64(maxInt64(s.compacts, 0)),
	}
	if info, err := os.Stat(s.dataPath); err == nil {
		stats.FileBytes = uint64(info.Size())
	}
	return stats
}

func (s *fileVectorStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *fileVectorStore) open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openLocked()
}

func (s *fileVectorStore) openLocked() error {
	if err := os.MkdirAll(s.basePath, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.dataPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	offsets, indexStats, err := rebuildFileVectorStoreIndex(file)
	if err != nil {
		_ = file.Close()
		return err
	}
	s.file = file
	s.offsets = offsets
	s.writes = indexStats.totalRecords
	s.stale = indexStats.staleRecords
	return s.maybeCompactLocked()
}

func (s *fileVectorStore) ensureOpenLocked() error {
	if s.file != nil {
		return nil
	}
	return s.openLocked()
}

func (s *fileVectorStore) maybeCompactLocked() error {
	if s.stale < fileVectorStoreCompactMinStaleRecords {
		return nil
	}
	if s.stale*fileVectorStoreCompactStaleDivisor < s.writes {
		return nil
	}
	return s.compactLocked()
}

func (s *fileVectorStore) compactLocked() error {
	if s.file == nil {
		return nil
	}

	live := make([]index.Vector, 0, len(s.offsets))
	for id, meta := range s.offsets {
		record, err := readFileVectorRecordAt(s.file, meta)
		if err != nil || record.op == fileVectorStoreOpDelete {
			continue
		}
		live = append(live, index.Vector{ID: id, Values: cloneVectorValues(record.values)})
	}

	tmpPath := s.dataPath + ".compact"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	newOffsets := make(map[string]fileVectorRecordMeta, len(live))
	var offset int64
	for _, vec := range live {
		record, err := encodeFileVectorRecord(fileVectorStoreOpPut, vec.ID, vec.Values)
		if err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		if _, err := tmpFile.Write(record); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return err
		}
		newOffsets[vec.ID] = fileVectorRecordMeta{
			recordOffset: offset,
			recordLength: uint32(len(record)),
		}
		offset += int64(len(record))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := s.file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.dataPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	file, err := os.OpenFile(s.dataPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return err
	}

	s.file = file
	s.offsets = newOffsets
	s.writes = int64(len(newOffsets))
	s.stale = 0
	s.compacts++
	return nil
}

type fileVectorRecord struct {
	op     byte
	id     string
	values []float64
}

func encodeFileVectorRecord(op byte, id string, values []float64) ([]byte, error) {
	idBytes := []byte(id)
	recordLength := 1 + 4 + len(idBytes) + 4 + len(values)*8
	buf := make([]byte, 4+recordLength)
	binary.LittleEndian.PutUint32(buf[:4], uint32(recordLength))
	pos := 4
	buf[pos] = op
	pos++
	binary.LittleEndian.PutUint32(buf[pos:pos+4], uint32(len(idBytes)))
	pos += 4
	copy(buf[pos:pos+len(idBytes)], idBytes)
	pos += len(idBytes)
	binary.LittleEndian.PutUint32(buf[pos:pos+4], uint32(len(values)))
	pos += 4
	for _, value := range values {
		binary.LittleEndian.PutUint64(buf[pos:pos+8], math.Float64bits(value))
		pos += 8
	}
	return buf, nil
}

type fileVectorStoreIndexStats struct {
	totalRecords int64
	staleRecords int64
}

func rebuildFileVectorStoreIndex(file *os.File) (map[string]fileVectorRecordMeta, fileVectorStoreIndexStats, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fileVectorStoreIndexStats{}, err
	}
	reader := bufio.NewReader(file)
	offsets := make(map[string]fileVectorRecordMeta)
	stats := fileVectorStoreIndexStats{}
	var offset int64

	for {
		recordOffset := offset
		lengthBuf := make([]byte, 4)
		_, err := io.ReadFull(reader, lengthBuf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fileVectorStoreIndexStats{}, err
		}
		recordLength := binary.LittleEndian.Uint32(lengthBuf)
		offset += 4

		payload := make([]byte, recordLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, fileVectorStoreIndexStats{}, err
		}
		offset += int64(recordLength)
		stats.totalRecords++

		record, err := decodeFileVectorPayload(payload)
		if err != nil {
			return nil, fileVectorStoreIndexStats{}, err
		}
		if record.op == fileVectorStoreOpDelete {
			stats.staleRecords++
			delete(offsets, record.id)
			continue
		}
		if _, exists := offsets[record.id]; exists {
			stats.staleRecords++
		}
		offsets[record.id] = fileVectorRecordMeta{
			recordOffset: recordOffset,
			recordLength: 4 + recordLength,
		}
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return nil, fileVectorStoreIndexStats{}, err
	}
	return offsets, stats, nil
}

func readFileVectorRecordAt(file *os.File, meta fileVectorRecordMeta) (fileVectorRecord, error) {
	buf := make([]byte, meta.recordLength)
	if _, err := file.ReadAt(buf, meta.recordOffset); err != nil {
		return fileVectorRecord{}, err
	}
	recordLength := binary.LittleEndian.Uint32(buf[:4])
	if uint32(len(buf)) < 4+recordLength {
		return fileVectorRecord{}, io.ErrUnexpectedEOF
	}
	return decodeFileVectorPayload(buf[4 : 4+recordLength])
}

func decodeFileVectorPayload(payload []byte) (fileVectorRecord, error) {
	if len(payload) < 9 {
		return fileVectorRecord{}, io.ErrUnexpectedEOF
	}
	pos := 0
	record := fileVectorRecord{op: payload[pos]}
	pos++

	idLen := int(binary.LittleEndian.Uint32(payload[pos : pos+4]))
	pos += 4
	if len(payload[pos:]) < idLen+4 {
		return fileVectorRecord{}, io.ErrUnexpectedEOF
	}
	record.id = string(payload[pos : pos+idLen])
	pos += idLen

	valuesLen := int(binary.LittleEndian.Uint32(payload[pos : pos+4]))
	pos += 4
	if len(payload[pos:]) < valuesLen*8 {
		return fileVectorRecord{}, io.ErrUnexpectedEOF
	}
	record.values = make([]float64, valuesLen)
	for i := 0; i < valuesLen; i++ {
		record.values[i] = math.Float64frombits(binary.LittleEndian.Uint64(payload[pos : pos+8]))
		pos += 8
	}
	return record, nil
}
