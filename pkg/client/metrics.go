package client

import (
	"io"
	"sync/atomic"
	"time"
)

// readerMetrics wraps io.ReadWriteCloser with simple metrics.
type readerMetrics struct {
	io.ReadWriteCloser

	nRead     atomic.Int64
	nWritten  atomic.Int64
	lastRead  atomicTime
	lastWrite atomicTime
}

// ReaderStats represents current statistics for readerMetrics.
type ReaderStats struct {
	BytesRead    int
	BytesWritten int
	LastReadAt   time.Time
	LastWriteAt  time.Time
}

func newReaderMetrics(rw io.ReadWriteCloser) *readerMetrics {
	return &readerMetrics{ReadWriteCloser: rw}
}

func (s *readerMetrics) BytesRead() int {
	return int(s.nRead.Load())
}

func (s *readerMetrics) BytesWritten() int {
	return int(s.nWritten.Load())
}

func (s *readerMetrics) Read(p []byte) (n int, err error) {
	n, err = s.ReadWriteCloser.Read(p)
	if err == nil {
		s.nRead.Add(int64(n))
		s.lastRead.Store(time.Now())
	}

	return n, err
}

func (s *readerMetrics) Write(p []byte) (n int, err error) {
	n, err = s.ReadWriteCloser.Write(p)
	if err == nil {
		s.nWritten.Add(int64(n))
		s.lastWrite.Store(time.Now())
	}

	return n, err
}

func (s *readerMetrics) Close() error {
	return s.ReadWriteCloser.Close()
}

// Stats returns snapshot of reader metrics.
func (s *readerMetrics) Stats() ReaderStats {
	return ReaderStats{
		BytesRead:    int(s.nRead.Load()),
		BytesWritten: int(s.nWritten.Load()),
		LastReadAt:   s.lastRead.Load(),
		LastWriteAt:  s.lastWrite.Load(),
	}
}

type atomicTime struct {
	value atomic.Int64
}

func (a *atomicTime) Store(t time.Time) {
	if t.IsZero() {
		a.value.Store(0)
		return
	}
	a.value.Store(t.UnixNano())
}

func (a *atomicTime) Load() time.Time {
	n := a.value.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
