package logging

import (
	"fmt"
	"sync"
	"time"
)

// TODO(wingo.zwt) Consider using logrus to refactor

func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)
}

func AsyncLogf(format string, args ...any) {
	once.Do(func() {
		asyncLogger = NewSimpleAsyncLogger()
	})
	msg := fmt.Sprintf(format, args...)
	asyncLogger.Log(msg)
}

type simpleLogPacket struct {
	msg string
}

type simpleAsyncLogger struct {
	logChan   chan *simpleLogPacket // logger buffer
	closeChan chan struct{}         // close signal
}

func NewSimpleAsyncLogger() *simpleAsyncLogger {
	l := &simpleAsyncLogger{
		logChan:   make(chan *simpleLogPacket, 1000),
		closeChan: make(chan struct{}),
	}
	go l.processLogLoop()
	return l
}

func (l *simpleAsyncLogger) Close() {
	close(l.closeChan)
}

func (l *simpleAsyncLogger) Log(msg string) {
	select {
	case l.logChan <- &simpleLogPacket{msg: msg}:
	default:
		// drop the log if the buffer is full
	}
}

func (l *simpleAsyncLogger) processLogLoop() {
	for {
		select {
		case p := <-l.logChan:
			fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), p.msg)
		case <-l.closeChan:
			return
		}
	}
}

var (
	asyncLogger *simpleAsyncLogger
	once        sync.Once
)
