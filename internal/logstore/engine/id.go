package engine

import "time"

// Composite ID layout: [segment_hour: 32 bits][chunk: 8 bits][row: 23 bits]
// Total: 63 bits (fits in int64, always positive).

const (
	rowBits   = 23
	chunkBits = 8
	rowMask   = (1 << rowBits) - 1   // 0x7FFFFF = 8,388,607
	chunkMask = (1 << chunkBits) - 1 // 0xFF = 255
	chunkSize = 50000                 // max entries per chunk
)

// EncodeID builds a composite int64 ID from segment hour, chunk number, and row offset.
func EncodeID(segmentHour int64, chunkNum, row int) int64 {
	return (segmentHour << (rowBits + chunkBits)) | (int64(chunkNum) << rowBits) | int64(row)
}

// DecodeID extracts segment hour, chunk number, and row offset from a composite ID.
func DecodeID(id int64) (segmentHour int64, chunkNum, row int) {
	row = int(id & rowMask)
	chunkNum = int((id >> rowBits) & chunkMask)
	segmentHour = id >> (rowBits + chunkBits)
	return
}

// SegmentHourFromTime returns the segment hour (hours since Unix epoch) for a given time.
func SegmentHourFromTime(t time.Time) int64 {
	return t.Unix() / 3600
}

// TimeFromSegmentHour converts a segment hour back to time (start of that hour).
func TimeFromSegmentHour(h int64) time.Time {
	return time.Unix(h*3600, 0).UTC()
}

// SegmentDirName returns the directory name for a segment hour (e.g., "2026-04-04T12").
func SegmentDirName(h int64) string {
	return TimeFromSegmentHour(h).Format("2006-01-02T15")
}

// IDForPosition computes the composite ID for a given position in the active WAL.
func IDForPosition(segmentHour int64, entryIndex int) int64 {
	chunkNum := entryIndex / chunkSize
	row := entryIndex % chunkSize
	return EncodeID(segmentHour, chunkNum, row)
}
