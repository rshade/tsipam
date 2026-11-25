package sqlitestore

import (
	"encoding/binary"
	"errors"
	"maps"
)

// The write-ahead log layout, from https://www.sqlite.org/fileformat2.html#walformat.
const (
	walHeaderSize      = 32
	walFrameHeaderSize = 24
	walMagic           = 0x377f0682
	walBigEndianSums   = 1
	walFormatVersion   = 3007000
	walVersionOffset   = 4
	walPageSizeOffset  = 8
	walSaltOffset      = 16
	walSaltSize        = 8
	walSumOffset       = 24
	frameCommitOffset  = 4
	frameSaltOffset    = 8
	frameSumOffset     = 16
	wordSize           = 4
	sumChunkSize       = 2 * wordSize

	// minPageSize is SQLite's smallest page; every size is a power of two.
	minPageSize = 512
	maxPageSize = 65536

	// The database header's write and read format versions. 2 means WAL.
	headerWriteVersion = 18
	headerReadVersion  = 19
	rollbackFormat     = 1
	walFormat          = 2
)

var errInvalidWAL = errors.New("the sqlite registry's write-ahead log does not match its database")

// applyWAL returns the database that database and its write-ahead log describe
// together, by SQLite's own recovery rule: frames apply up to the last commit,
// and the first frame with a stale salt or a wrong checksum ends the log. The
// result is in rollback format, so it opens without a log.
func applyWAL(database, wal []byte) ([]byte, error) {
	pages, size, err := committedPages(wal)
	if err != nil {
		return nil, err
	}
	image := append([]byte{}, database...)
	if len(pages) > 0 {
		if len(image) < size {
			image = append(image, make([]byte, size-len(image))...)
		}
		for offset, page := range pages {
			if offset < size {
				copy(image[offset:], page)
			}
		}
		image = image[:size]
	}
	if len(image) > headerReadVersion && image[headerReadVersion] == walFormat {
		image[headerWriteVersion], image[headerReadVersion] = rollbackFormat, rollbackFormat
	}
	return image, nil
}

// committedPages returns the newest committed content of each page in wal,
// keyed by its offset in the database, and the database's size after the last
// commit.
func committedPages(wal []byte) (map[int][]byte, int, error) {
	if len(wal) < walHeaderSize {
		return nil, 0, nil
	}
	header := wal[:walHeaderSize]
	magic := binary.BigEndian.Uint32(header)
	if magic&^walBigEndianSums != walMagic {
		return nil, 0, nil
	}
	var order binary.ByteOrder = binary.LittleEndian
	if magic&walBigEndianSums != 0 {
		order = binary.BigEndian
	}
	sum := checksum(order, [2]uint32{}, header[:walSumOffset])
	if sum != storedSum(header[walSumOffset:]) {
		return nil, 0, nil
	}
	pageSize := int(binary.BigEndian.Uint32(header[walPageSizeOffset:]))
	if binary.BigEndian.Uint32(header[walVersionOffset:]) != walFormatVersion ||
		pageSize < minPageSize || pageSize > maxPageSize || pageSize&(pageSize-1) != 0 {
		return nil, 0, errInvalidWAL
	}
	salt := string(header[walSaltOffset : walSaltOffset+walSaltSize])
	committed, pending := map[int][]byte{}, map[int][]byte{}
	size := 0
	frameSize := walFrameHeaderSize + pageSize
	for rest := wal[walHeaderSize:]; len(rest) >= frameSize; rest = rest[frameSize:] {
		frame, page := rest[:walFrameHeaderSize], rest[walFrameHeaderSize:frameSize]
		number := int(binary.BigEndian.Uint32(frame))
		sum = checksum(order, sum, frame[:frameSaltOffset])
		sum = checksum(order, sum, page)
		if number == 0 || string(frame[frameSaltOffset:frameSaltOffset+walSaltSize]) != salt ||
			sum != storedSum(frame[frameSumOffset:]) {
			break
		}
		pending[(number-1)*pageSize] = page
		if count := int(binary.BigEndian.Uint32(frame[frameCommitOffset:])); count != 0 {
			maps.Copy(committed, pending)
			clear(pending)
			size = count * pageSize
		}
	}
	return committed, size, nil
}

func storedSum(data []byte) [2]uint32 {
	return [2]uint32{binary.BigEndian.Uint32(data), binary.BigEndian.Uint32(data[wordSize:])}
}

// checksum continues SQLite's running WAL checksum over data.
func checksum(order binary.ByteOrder, sum [2]uint32, data []byte) [2]uint32 {
	for ; len(data) >= sumChunkSize; data = data[sumChunkSize:] {
		sum[0] += order.Uint32(data) + sum[1]
		sum[1] += order.Uint32(data[wordSize:]) + sum[0]
	}
	return sum
}
