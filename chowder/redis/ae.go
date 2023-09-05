package redis

import (
	"container/list"
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type FileProc func(eventLoop *EventLoop, fd int, clientData interface{}, mask int)
type TimeProc func(eventLoop *EventLoop, id int, clientData interface{}) (time.Time, IDType)
type EventFinalizerProc func(eventLoop *EventLoop, clientData interface{})
type BeforeSleepProc func(eventLoop *EventLoop)

type FileEvent struct {
	mask       Action
	wfileProc  FileProc
	rfileProc  FileProc
	clientData interface{}
}

type FiredEvent struct {
	fd   int
	mask int
}

type EventLoop struct {
	maxFd           int // highest file descriptor currently registered
	setSize         int // max number of file descriptors tracked
	timeEventNextId int
	lastTime        int
	events          []FileEvent
	fired           []FiredEvent
	timeEventHead   *list.List //TimeEvent
	stop            bool
	apidata         *apiState
	beforeSleep     BeforeSleepProc
	afterSleep      BeforeSleepProc
	flags           Event
}

// 初始化函数
func Create(setSize int) *EventLoop {
	return &EventLoop{
		setSize: setSize,
		maxFd:   -1,
		events:  make([]FileEvent, setSize),
		fired:   make([]FiredEvent, setSize),
	}
}

func (el *EventLoop) GetSetSize() int {
	return el.setSize
}

func (el *EventLoop) SetDontWait(noWait bool) {
	if noWait {
		el.flags |= DONT_WAIT
		return
	}
	el.flags &= ^DONT_WAIT
}

func (el *EventLoop) Resize(setSize int) error {
	if setSize == el.setSize {
		return nil
	}

	if el.maxFd >= setSize {
		return errors.New("TODO 定义下错误消息")
	}

	el.apiResize(setSize)

	el.events = append([]FileEvent{}, el.events[:setSize]...)
	el.fired = append([]FiredEvent{}, el.fired[:setSize]...)
	el.setSize = setSize

	for i := el.maxFd + 1; i < setSize; i++ {
		el.events[i].mask = NONE
	}
	return nil
}

func (el *EventLoop) Delete() {
	el.events = nil
	el.fired = nil
	//TODO 定时器链表直接给个空

}

func (el *EventLoop) Stop() {
	el.stop = true

}

func (el *EventLoop) CreateFileEvent(fd int, mask Action, proc FileProc, clientData interface{}) error {
	if fd >= el.setSize {
		return errors.New("create file event fail")
	}

	fe := &el.events[fd]

	if err := el.apiAddEvent(fd, mask); err != nil {
		return err
	}

	fe.mask |= mask

	if mask&READABLE > 0 {
		fe.rfileProc = proc
	}

	if mask&WRITABLE > 0 {
		fe.wfileProc = proc
	}

	fe.clientData = clientData

	if fd > el.maxFd {
		el.maxFd = fd
	}

	return nil
}

func (el *EventLoop) DeleteFileEvent(fd int, mask Action) {
	if fd >= el.setSize {
		return
	}

	fe := el.events[fd]

	if fe.mask == NONE {
		return
	}

	el.apiDelEvent(fd, mask)
	fe.mask = fe.mask & (^mask)

	if fd == el.maxFd && fe.mask == NONE {

		j := 0
		for j = el.maxFd - 1; j >= 0; j-- {
			if el.events[j].mask != NONE {
				break
			}
		}

		el.maxFd = j
	}

	return
}

func (el *EventLoop) GetFileEvents(fd int) Action {
	if fd >= el.setSize {
		return 0
	}

	fe := el.events[fd]
	return fe.mask
}

func GetTime() time.Time {
	return time.Now()
}

func (el *EventLoop) CreateTimeEvent(milliseconds time.Duration, proc TimeProc, clientData interface{}, finalizerProc EventFinalizerProc) {
	id := el.timeEventNextId
	el.timeEventNextId++

	addTimeEvent(el.timeEventHead, id, milliseconds, proc, clientData, finalizerProc)
}

func (el *EventLoop) DeleteTimeEvent(id int) error {
	for e := el.timeEventHead.Front(); e != nil; e = e.Next() {

		te := e.Value.(*TimeEvent)

		if te.id == id {
			te.id = DELETED_EVENT_ID
			return nil
		}
	}

	return ERR

}

func (el *EventLoop) usUntilEarliestTimer() (rv time.Duration, exists bool) {
	if el.timeEventHead == nil {
		return time.Duration(0), false
	}

	var earliest *TimeEvent
	for e := el.timeEventHead.Front(); e != nil; e = e.Next() {
		et := e.Value.(*TimeEvent)
		if earliest == nil || earliest.when.Before(et.when) {
			earliest = et
		}
	}

	now := time.Now()
	if now.Before(earliest.when) {
		return time.Duration(0), false
	}

	return now.Sub(earliest.when), true
}

func (el *EventLoop) ProcessTimeEvents() int {
	now := time.Now()
	processed := 0

	for e := el.timeEventHead.Front(); e != nil; {
		next := e.Next()
		te := e.Value.(*TimeEvent)

		if te.id == DELETED_EVENT_ID {
			if te.refcount > 0 {
				goto next
			}

			el.timeEventHead.Remove(e)
			goto next
		}

		if te.id > el.maxFd {
			goto next
		}

		// te.when <= now
		if te.when.Before(now) || te.when.Equal(now) {
			id := te.id
			to, retVal := te.timeProc(el, id, te.clientData)
			te.refcount--
			processed++
			now = time.Now()
			if retVal != NOMORE {
				te.when = to
			} else {
				te.id = DELETED_EVENT_ID
			}
		}

	next:
		e = next
	}

	return processed
}

func (el *EventLoop) ProcessEvents(flags Event) int {
	processed := 0

	if !(flags&TIME_EVENTS > 0) || !(flags&FILE_EVENTS > 0) {
		return 0
	}

	var tv time.Duration

	if el.maxFd != -1 || flags&TIME_EVENTS > 0 && flags&DONT_WAIT == 0 {

		if flags&TIME_EVENTS > 0 && flags&DONT_WAIT == 0 {
			tv, _ = el.usUntilEarliestTimer()
		}

		if tv < 0 {
			if flags&DONT_WAIT > 0 {
				tv = time.Duration(0)
			}
		}

		if el.flags&DONT_WAIT > 0 {
			tv = time.Duration(0)
		}

		if el.beforeSleep != nil && flags&CALL_BEFORE_SLEEP > 0 {
			el.beforeSleep(el)
		}

		numevents := el.apiPoll(tv)

		if el.afterSleep != nil && flags&CALL_AFTER_SLEEP > 0 {
			el.afterSleep(el)
		}

		for j := 0; j < numevents; j++ {
			fe := el.events[el.fired[j].fd]
			mask := el.fired[j].mask
			fd := el.fired[j].fd
			fired := 0

			//TOOD 未知
			invert := fe.mask&BARRIER > 0

			//TODO判断
			if !invert && fe.mask&READABLE > 0 && mask&READABLE > 0 {
				fe.rfileProc(el, fd, fe.clientData, mask)
				fired++
				fe = el.events[fd]
			}

			if fe.mask&WRITABLE > 0 && mask&WRITABLE > 0 {
				if fired == 0 || *(*uintptr)(unsafe.Pointer(&fe.wfileProc)) != *(*uintptr)(unsafe.Pointer(&fe.rfileProc)) {
					fe.wfileProc(el, fd, fe.clientData, mask)
					fired++
				}
			}

			if invert {
				fe = el.events[fd]
				if (fe.mask&READABLE > 0 && mask&READABLE > 0) &&
					(fired == 0 || *(*uintptr)(unsafe.Pointer(&fe.wfileProc)) != *(*uintptr)(unsafe.Pointer(&fe.rfileProc))) {

					fe.wfileProc(el, fd, fe.clientData, mask)
					fired++
				}
			}
			processed++
		}
	}

	if flags&TIME_EVENTS > 0 {
		processed += el.ProcessTimeEvents()
	}

	return processed
}

func Wait(fd int, mask int, milliseconds int) (Action, error) {
	pfd := make([]unix.PollFd, 1)
	pfd[0].Fd = int32(fd)

	reMask := Action(0)
	if mask&READABLE > 0 {
		reMask |= unix.POLLIN
	}

	if mask&WRITABLE > 0 {
		reMask |= unix.POLLOUT
	}

	retVal, err := unix.Poll(pfd, milliseconds)
	if retVal == 1 {
		if pfd[0].Revents&unix.POLLIN > 0 {
			reMask |= READABLE
		}
		if pfd[0].Revents&unix.POLLOUT > 0 {
			reMask |= WRITABLE
		}
		if pfd[0].Revents&unix.POLLERR > 0 {
			reMask |= WRITABLE
		}
		if pfd[0].Revents&unix.POLLHUP > 0 {
			reMask |= WRITABLE
		}

		return reMask, nil
	}

	return 0, err
}

func (el *EventLoop) Main() {
	for !el.stop {
		if el.beforeSleep != nil {
			el.beforeSleep(el)
		}
		el.ProcessEvents(ALL_EVENTS | CALL_BEFORE_SLEEP | CALL_AFTER_SLEEP)
	}
}

func (el *EventLoop) GetApiName() string {
	return apiName()
}

func (el *EventLoop) SetBeforeSleepProc(beforeSleep BeforeSleepProc) {
	el.beforeSleep = beforeSleep
}

func (el *EventLoop) SetAfterSleepProc(afterSleep BeforeSleepProc) {
	el.afterSleep = afterSleep
}
