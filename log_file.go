package main

import (
	"io"
	"log"
	"os"
)

type LogFile struct {
	file     *os.File
	Filename string
	offset   int64
}

func NewLogFile(filename string, initialOffset int64) (*LogFile, error) {
	f, err := os.Open(filename)

	if err != nil {
		return nil, err
	}

	var offset int64
	if initialOffset > 0 {
		offset, err = f.Seek(initialOffset, os.SEEK_SET)
		if err != nil {
			return nil, err
		}
	}

	logFile := &LogFile{
		file:     f,
		Filename: filename,
		offset:   offset,
	}

	return logFile, nil
}

func (f *LogFile) ReadNewLines() ([]byte, error) {
	stat, err := f.file.Stat()
	if err != nil {
		return nil, err
	}
	bytesToRead := stat.Size() - f.offset
	buf := make([]byte, bytesToRead)
	n, err := f.file.Read(buf)
	f.offset = stat.Size()
	log.Printf("Read: %d, try: %d, err: %v", n, bytesToRead, err)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (f *LogFile) GetOffset() int64 {
	return f.offset
}

func (f *LogFile) Close() {
	if f.file != nil {
		f.file.Close()
		f.file = nil
	}
}
