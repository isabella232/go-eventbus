package eventbus

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p-testing/race"
)

type EventA struct{}
type EventB int

func getN() int {
	n := 50000
	if race.WithRace() {
		n = 1000
	}
	return n
}

func (EventA) String() string {
	return "Oh, Hello"
}

func TestEmit(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe(new(EventA))
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		defer sub.Close()
		<-sub.Out()
	}()

	em, err := bus.Emitter(new(EventA))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	em.Emit(EventA{})
}

func TestSub(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe(new(EventB))
	if err != nil {
		t.Fatal(err)
	}

	var event EventB

	var wait sync.WaitGroup
	wait.Add(1)

	go func() {
		defer sub.Close()
		event = (<-sub.Out()).(EventB)
		wait.Done()
	}()

	em, err := bus.Emitter(new(EventB))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	em.Emit(EventB(7))
	wait.Wait()

	if event != 7 {
		t.Error("got wrong event")
	}
}

func TestEmitNoSubNoBlock(t *testing.T) {
	bus := NewBus()

	em, err := bus.Emitter(new(EventA))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	em.Emit(EventA{})
}

func TestEmitOnClosed(t *testing.T) {
	bus := NewBus()

	em, err := bus.Emitter(new(EventA))
	if err != nil {
		t.Fatal(err)
	}
	em.Close()

	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic")
		}
		if r.(string) != "emitter is closed" {
			t.Error("unexpected message")
		}
	}()

	em.Emit(EventA{})
}

func TestClosingRaces(t *testing.T) {
	subs := getN()
	emits := getN()

	var wg sync.WaitGroup
	var lk sync.RWMutex
	lk.Lock()

	wg.Add(subs + emits)

	b := NewBus()

	for i := 0; i < subs; i++ {
		go func() {
			lk.RLock()
			defer lk.RUnlock()

			sub, _ := b.Subscribe(new(EventA))
			time.Sleep(10 * time.Millisecond)
			sub.Close()

			wg.Done()
		}()
	}
	for i := 0; i < emits; i++ {
		go func() {
			lk.RLock()
			defer lk.RUnlock()

			emit, _ := b.Emitter(new(EventA))
			time.Sleep(10 * time.Millisecond)
			emit.Close()

			wg.Done()
		}()
	}

	time.Sleep(10 * time.Millisecond)
	lk.Unlock() // start everything

	wg.Wait()

	if len(b.(*basicBus).nodes) != 0 {
		t.Error("expected no nodes")
	}
}

func TestSubMany(t *testing.T) {
	bus := NewBus()

	var r int32

	n := getN()
	var wait sync.WaitGroup
	var ready sync.WaitGroup
	wait.Add(n)
	ready.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			sub, err := bus.Subscribe(new(EventB))
			if err != nil {
				panic(err)
			}
			defer sub.Close()

			ready.Done()
			atomic.AddInt32(&r, int32((<-sub.Out()).(EventB)))
			wait.Done()
		}()
	}

	em, err := bus.Emitter(new(EventB))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	ready.Wait()

	em.Emit(EventB(7))
	wait.Wait()

	if int(r) != 7*n {
		t.Error("got wrong result")
	}
}

func TestSubType(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe([]interface{}{new(EventA), new(EventB)})
	if err != nil {
		t.Fatal(err)
	}

	var event fmt.Stringer

	var wait sync.WaitGroup
	wait.Add(1)

	go func() {
		defer sub.Close()
		event = (<-sub.Out()).(EventA)
		wait.Done()
	}()

	em, err := bus.Emitter(new(EventA))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	em.Emit(EventA{})
	wait.Wait()

	if event.String() != "Oh, Hello" {
		t.Error("didn't get the correct message")
	}
}

func TestNonStateful(t *testing.T) {
	bus := NewBus()
	em, err := bus.Emitter(new(EventB))
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	sub1, err := bus.Subscribe(new(EventB), BufSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer sub1.Close()

	select {
	case <-sub1.Out():
		t.Fatal("didn't expect to get an event")
	default:
	}

	em.Emit(EventB(1))

	select {
	case e := <-sub1.Out():
		if e.(EventB) != 1 {
			t.Fatal("got wrong event")
		}
	default:
		t.Fatal("expected to get an event")
	}

	sub2, err := bus.Subscribe(new(EventB), BufSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()

	select {
	case <-sub2.Out():
		t.Fatal("didn't expect to get an event")
	default:
	}
}

func TestStateful(t *testing.T) {
	bus := NewBus()
	em, err := bus.Emitter(new(EventB), Stateful)
	if err != nil {
		t.Fatal(err)
	}
	defer em.Close()

	em.Emit(EventB(2))

	sub, err := bus.Subscribe(new(EventB), BufSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if (<-sub.Out()).(EventB) != 2 {
		t.Fatal("got wrong event")
	}
}

func testMany(t testing.TB, subs, emits, msgs int, stateful bool) {
	if race.WithRace() && subs+emits > 5000 {
		t.SkipNow()
	}

	bus := NewBus()

	var r int64

	var wait sync.WaitGroup
	var ready sync.WaitGroup
	wait.Add(subs + emits)
	ready.Add(subs)

	for i := 0; i < subs; i++ {
		go func() {
			sub, err := bus.Subscribe(new(EventB))
			if err != nil {
				panic(err)
			}
			defer sub.Close()

			ready.Done()
			for i := 0; i < emits*msgs; i++ {
				e, ok := <-sub.Out()
				if !ok {
					panic("wat")
				}
				atomic.AddInt64(&r, int64(e.(EventB)))
			}
			wait.Done()
		}()
	}

	for i := 0; i < emits; i++ {
		go func() {
			em, err := bus.Emitter(new(EventB), func(settings interface{}) error {
				settings.(*emitterSettings).makeStateful = stateful
				return nil
			})
			if err != nil {
				panic(err)
			}
			defer em.Close()

			ready.Wait()

			for i := 0; i < msgs; i++ {
				em.Emit(EventB(97))
			}

			wait.Done()
		}()
	}

	wait.Wait()

	if int(r) != 97*subs*emits*msgs {
		t.Fatal("got wrong result")
	}
}

func TestBothMany(t *testing.T) {
	testMany(t, 10000, 100, 10, false)
}

type benchCase struct {
	subs     int
	emits    int
	stateful bool
}

func (bc benchCase) name() string {
	return fmt.Sprintf("subs-%d/emits-%d/stateful-%t", bc.subs, bc.emits, bc.stateful)
}

func genTestCases() []benchCase {
	ret := make([]benchCase, 0, 200)
	for stateful := 0; stateful < 2; stateful++ {
		for subs := uint(0); subs <= 8; subs = subs + 2 {
			for emits := uint(0); emits <= 8; emits = emits + 2 {
				ret = append(ret, benchCase{1 << subs, 1 << emits, stateful == 1})
			}
		}
	}
	return ret
}

func BenchmarkEvents(b *testing.B) {
	for _, bc := range genTestCases() {
		b.Run(bc.name(), benchMany(bc))
	}
}

func benchMany(bc benchCase) func(*testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		subs := bc.subs
		emits := bc.emits
		stateful := bc.stateful
		bus := NewBus()
		var wait sync.WaitGroup
		var ready sync.WaitGroup
		wait.Add(subs + emits)
		ready.Add(subs + emits)

		for i := 0; i < subs; i++ {
			go func() {
				sub, err := bus.Subscribe(new(EventB))
				if err != nil {
					panic(err)
				}
				defer sub.Close()

				ready.Done()
				ready.Wait()
				for i := 0; i < (b.N/emits)*emits; i++ {
					_, ok := <-sub.Out()
					if !ok {
						panic("wat")
					}
				}
				wait.Done()
			}()
		}

		for i := 0; i < emits; i++ {
			go func() {
				em, err := bus.Emitter(new(EventB), func(settings interface{}) error {
					settings.(*emitterSettings).makeStateful = stateful
					return nil
				})
				if err != nil {
					panic(err)
				}
				defer em.Close()

				ready.Done()
				ready.Wait()

				for i := 0; i < b.N/emits; i++ {
					em.Emit(EventB(97))
				}

				wait.Done()
			}()
		}
		ready.Wait()
		b.ResetTimer()
		wait.Wait()
	}
}
