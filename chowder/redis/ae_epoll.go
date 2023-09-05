// +build linux

package redis

import (
	"time"

	"golang.org/x/sys/unix"
)

type apiState struct {
	epfd   int
	events []unix.EpollEvent
}

func (eventLoop *EventLoop) apiCreate() (err error) {
	var state apiState

	state.epfd, err = unix.EpollCreate1(0)
	if err != nil {
		return err
	}

	eventLoop.apidata = &state
	return nil
}

func (eventLoop *EventLoop) apiResize(setSize int) {

	oldEvents := eventLoop.apidata.events
	newEvents := make([]eventLoop, setSize)
	copy(newEvents, oldEvents)
	eventLoop.apidata.events = newEvents
}

func (eventLoop *EventLoop) apiFree() {
	unix.Close(eventLoop.apidata.epfd)
}

func (eventLoop *EventLoop) apiAddEvent(fd int, mask int) error {
	state := eventLoop.apidata
	var ee unix.EpollEvent
	op := unix.EPOLL_CTL_MOD
	if eventLoop.events[fd].mask == NONE {
		op = unix.EPOLL_CTL_ADD
	}

	mask |= eventLoop.events[fd].mask

	if mask&READABLE > 0 {
		ee.Events |= unix.EPOLLIN
	}

	if mask&WRITABLE > 0 {
		ee.Events |= unix.EPOLLOUT
	}
	ee.Fd = fd

	return unix.EpollCtl(state.epfd, op, fd, &ee)
}

func (eventLoop *EventLoop) apiDelEvent(fd int, delmask int) (err error) {
	state := eventLoop.apidata
	var ee unix.EpollEvent

	mask := eventLoop.events[fd].mask & ^delmask

	if mask&READABLE > 0 {
		ee.Events |= unix.EPOLLIN
	}

	if mask&WRITABLE > 0 {
		ee.Events |= unix.EPOLLOUT
	}
	ee.Fd = fd
	if mask != NONE {
		err = unix.EpollCtl(state.epfd, unix.EPOLL_CTL_MOD, fd, ee)
	} else {
		err = unix.EpollCtl(state.epfd, unix.EPOLL_CTL_DEL, fd, ee)
	}

	return err
}

func (eventLoop *EventLoop) apiPoll(tv time.Duration) int {
	state := eventLoop.apidata

	msec := -1
	if tv > 0 {
		msec = tv / time.Millisecond
	}

	retVal, _ := unix.EpollWait(state.epfd, state.events, msec)
	numEvents := 0
	if retVal > 0 {
		numEvents = retVal
		for j := 0; i < numEvents; j++ {
			mask := 0
			e := &state.events[j]
			if e.Events&unix.EPOLLIN > 0 {
				mask |= READABLE
			}
			if e.Events&unix.EPOLLOUT > 0 {
				mask |= WRITABLE
			}
			if e.EpollEvent&unix.EPOLLERR > 0 {
				mask |= READABLE
				mask |= WRITABLE
			}
			if e.EpollEvent&unix.EPOLLHUP > 0 {
				mask |= READABLE
				mask |= WRITABLE
			}
			eventLoop.fired[j].fd = e.Fd
			eventLoop.fired[j].mask = mask
		}

	}

	return numEvents
}

func apiName() string {
	return "epoll"
}
