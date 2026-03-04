package forwarder

import (
	"context"
	"errors"
	"io"
	"llumnix/pkg/consts"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock Reader for testing.
type mockReader struct {
	mock.Mock
	data  []byte
	delay time.Duration
}

func (m *mockReader) Read(p []byte) (int, error) {
	args := m.Called(p)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	n := copy(p, m.data)
	return n, args.Error(1)
}

func (m *mockReader) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestTimeoutReader_ReadFromRegularReader_Success(t *testing.T) {
	data := "hello world"
	reader := io.NopCloser(strings.NewReader(data))
	tr := &TimeoutReader{
		r:       reader,
		timeout: 5 * time.Second,
		ctx:     context.Background(),
	}

	buf := make([]byte, 20)
	n, err := tr.Read(buf)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, string(buf[:n]))
}

func TestTimeoutReader_ReadFromRegularReader_Timeout(t *testing.T) {
	mockReader := &mockReader{
		data:  []byte("hello"),
		delay: 200 * time.Millisecond,
	}
	mockReader.On("Read", mock.Anything).Return(5, nil)

	tr := &TimeoutReader{
		r:       mockReader,
		timeout: 50 * time.Millisecond,
		ctx:     context.Background(),
	}

	buf := make([]byte, 10)
	start := time.Now()
	n, err := tr.Read(buf)
	elapsed := time.Since(start)

	assert.Equal(t, consts.ErrorReadTimeout, err)
	assert.Equal(t, 0, n)
	assert.True(t, elapsed >= 50*time.Millisecond)
	assert.True(t, elapsed < 100*time.Millisecond)
}

func TestTimeoutReader_ReadFromRegularReader_Error(t *testing.T) {
	expectedErr := errors.New("read error")
	mockReader := &mockReader{delay: 10 * time.Millisecond}
	mockReader.On("Read", mock.Anything).Return(0, expectedErr)

	tr := &TimeoutReader{
		r:       mockReader,
		timeout: 100 * time.Millisecond,
		ctx:     context.Background(),
	}

	buf := make([]byte, 10)
	n, err := tr.Read(buf)

	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 0, n)
	mockReader.AssertExpectations(t)
}

func TestTimeoutReader_ReadFromRegularReader_EOF(t *testing.T) {
	mockReader := &mockReader{delay: 10 * time.Millisecond}
	mockReader.On("Read", mock.Anything).Return(0, io.EOF)

	tr := &TimeoutReader{
		r:       mockReader,
		timeout: 100 * time.Millisecond,
		ctx:     context.Background(),
	}

	buf := make([]byte, 10)
	n, err := tr.Read(buf)

	assert.Equal(t, io.EOF, err)
	assert.Equal(t, 0, n)
	mockReader.AssertExpectations(t)
}

func TestTimeoutReader_ReadFromRegularReader_PartialRead(t *testing.T) {
	data := []byte("hello")
	mockReader := &mockReader{
		data:  data,
		delay: 10 * time.Millisecond,
	}
	mockReader.On("Read", mock.Anything).Return(len(data), nil)

	tr := &TimeoutReader{
		r:       mockReader,
		timeout: 100 * time.Millisecond,
		ctx:     context.Background(),
	}

	buf := make([]byte, 10)
	n, err := tr.Read(buf)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, string(data), string(buf[:n]))
	mockReader.AssertExpectations(t)
}

func TestTimeoutReader_ReadWithBufferSmallerThanData(t *testing.T) {
	longData := "this is a very long string that exceeds small buffer size"
	reader := io.NopCloser(strings.NewReader(longData))
	tr := &TimeoutReader{
		r:       reader,
		timeout: 100 * time.Millisecond,
		ctx:     context.Background(),
	}

	smallBuf := make([]byte, 10)
	n, err := tr.Read(smallBuf)

	assert.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, longData[:10], string(smallBuf[:n]))
}

func TestTimeoutReader_Close(t *testing.T) {
	mReader := &mockReader{}
	mReader.On("Close").Return(nil)

	err := mReader.Close()
	assert.NoError(t, err)
	mReader.AssertExpectations(t)
}
