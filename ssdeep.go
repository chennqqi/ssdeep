package ssdeep

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	rollingWindow uint32 = 7
	blockMin      int64  = 3
	spamSumLength        = 64
	minFileSize          = 4096
	hashPrime     uint32 = 0x01000193
	hashInit      uint32 = 0x28021967
	b64String            = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

var b64 = []byte(b64String)
var ErrSmallInput = errors.New("Too small data size")
var ErrSmallBlock = errors.New("Too small block size")

type rollingState struct {
	window []byte
	h1     uint32
	h2     uint32
	h3     uint32
	n      uint32
}

func (rs rollingState) rollSum() uint32 {
	return rs.h1 + rs.h2 + rs.h3
}

type ssdeepState struct {
	rollingState rollingState
	blockSize    int64
	hashString1  string
	hashString2  string
	blockHash1   uint32
	blockHash2   uint32
}

func newSsdeepState() ssdeepState {
	return ssdeepState{
		blockHash1: hashInit,
		blockHash2: hashInit,
		rollingState: rollingState{
			window: make([]byte, rollingWindow),
		},
	}
}

func (state *ssdeepState) newRollingState() {
	state.rollingState = rollingState{}
	state.rollingState.window = make([]byte, rollingWindow)
}

// sumHash based on FNV hash
func sumHash(c byte, h uint32) uint32 {
	return (h * hashPrime) ^ uint32(c)
}

// rollHash based on Adler checksum
func (state *ssdeepState) rollHash(c byte) {
	rs := &state.rollingState
	rs.h2 -= rs.h1
	rs.h2 += rollingWindow * uint32(c)
	rs.h1 += uint32(c)
	rs.h1 -= uint32(rs.window[rs.n])
	rs.window[rs.n] = c
	rs.n++
	if rs.n == rollingWindow {
		rs.n = 0
	}
	rs.h3 = rs.h3 << 5
	rs.h3 ^= uint32(c)
}

// getBlockSize calculates the block size based on file size
func (state *ssdeepState) getBlockSize(n int64) {
	blockSize := blockMin
	for blockSize*spamSumLength < n {
		blockSize = blockSize * 2
	}
	state.blockSize = blockSize
}

func (state *ssdeepState) processByte(b byte) {
	state.blockHash1 = sumHash(b, state.blockHash1)
	state.blockHash2 = sumHash(b, state.blockHash2)
	state.rollHash(b)
	rh := int64(state.rollingState.rollSum())
	if rh%state.blockSize == (state.blockSize - 1) {
		if len(state.hashString1) < spamSumLength-1 {
			state.hashString1 += string(b64[state.blockHash1%64])
			state.blockHash1 = hashInit
		}
		if rh%(state.blockSize*2) == ((state.blockSize * 2) - 1) {
			if len(state.hashString2) < spamSumLength/2-1 {
				state.hashString2 += string(b64[state.blockHash2%64])
				state.blockHash2 = hashInit
			}
		}
	}
}

// Reader is the minimum interface that ssdeep needs in order to calculate the fuzzy hash.
// Reader groups io.Seeker and io.Reader.
type Reader interface {
	io.Seeker
	io.Reader
}

func (state *ssdeepState) process(r *bufio.Reader) {
	state.newRollingState()
	b, err := r.ReadByte()
	for err == nil {
		state.processByte(b)
		b, err = r.ReadByte()
	}
}

// FuzzyReader computes the fuzzy hash of a Reader interface with a given input size.
// It is the caller's responsibility to append the filename, if any, to result after computation.
// Returns an error when ssdeep could not be computed on the Reader.
func FuzzyReader(f Reader, size int64) (string, error) {
	if size < minFileSize {
		return "", ErrSmallInput
	}
	state := newSsdeepState()
	state.getBlockSize(size)
	for {
		f.Seek(0, 0)
		r := bufio.NewReader(f)
		state.process(r)
		if state.blockSize < blockMin {
			return "", ErrSmallBlock
		}
		if len(state.hashString1) < spamSumLength/2 {
			state.blockSize = state.blockSize / 2
			state.blockHash1 = hashInit
			state.blockHash2 = hashInit
			state.hashString1 = ""
			state.hashString2 = ""
		} else {
			rh := state.rollingState.rollSum()
			if rh != 0 {
				// Finalize the hash string with the remaining data
				state.hashString1 += string(b64[state.blockHash1%64])
				state.hashString2 += string(b64[state.blockHash2%64])
			}
			break
		}
	}
	return fmt.Sprintf("%d:%s:%s", state.blockSize, state.hashString1, state.hashString2), nil
}

// FuzzyFilename computes the fuzzy hash of a file.
// FuzzyFilename will opens, reads, and hashes the contents of the file 'filename'.
// It is the caller's responsibility to append the filename to the result after computation.
// Returns an error when the file doesn't exist or ssdeep could not be computed on the file.
func FuzzyFilename(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	return FuzzyFile(f)
}

// FuzzyFile computes the fuzzy hash of a file using os.File pointer.
// FuzzyFile will computes the fuzzy hash of the contents of the open file, starting at the beginning of the file.
// When finished, the file pointer is returned to its original position.
// If an error occurs, the file pointer's value is undefined.
// It is the callers's responsibility to append the filename to the result after computation.
// Returns an error when ssdeep could not be computed on the file.
func FuzzyFile(f *os.File) (string, error) {
	currentPosition, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}

	f.Seek(0, io.SeekStart)
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	result, err := FuzzyReader(f, stat.Size())
	if err != nil {
		return "", err
	}

	f.Seek(currentPosition, io.SeekStart)
	return result, nil
}

// FuzzyBytes computes the fuzzy hash of a slice of byte.
// It is the caller's responsibility to append the filename, if any, to result after computation.
// Returns an error when ssdeep could not be computed on the buffer.
func FuzzyBytes(buffer []byte) (string, error) {
	n := len(buffer)
	br := bytes.NewReader(buffer)

	result, err := FuzzyReader(br, int64(n))
	if err != nil {
		return "", err
	}

	return result, nil
}
