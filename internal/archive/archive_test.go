package archive

import (
	"testing"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/pdu"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type fakeStorage struct{ records []pdu.Record }

func (f fakeStorage) ListStorage(storage string) ([]pdu.Record, error) {
	if storage == "SM" {
		return f.records, nil
	}
	return nil, nil
}
func (f fakeStorage) DeleteMessages(string, []int) error { return nil }

type fakeSIM struct{}

func (fakeSIM) CurrentID() int64 { return store.UnknownSIM }

func TestArchiveQueuesOnlyFreshInbound(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newArchiver(fakeStorage{records: []pdu.Record{{Index: 1, Sender: "10086",
		Timestamp: "2026-10-15 12:00:00", Text: "余额提醒", ConcatRef: -1}}},
		st, fakeSIM{}, false, []string{"bark", "telegram"})
	for i := 0; i < 2; i++ {
		if err := a.SyncIfStale(0); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("same hardware message should create two channel jobs only, got %d", len(jobs))
	}
}

func TestArchiveSetChannelsBeforeFirstSync(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newArchiver(fakeStorage{records: []pdu.Record{{Index: 1, Sender: "10086", Timestamp: "2026-10-15 12:01:00", Text: "new", ConcatRef: -1}}}, st, fakeSIM{}, false, nil)
	channels := []string{"bark", "telegram"}
	a.SetChannels(channels)
	channels[0] = "changed"
	if err := a.SyncIfStale(0); err != nil {
		t.Fatal(err)
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].Channel != "bark" {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestArchivePauseStillArchivesWithoutEnqueue(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newArchiver(fakeStorage{records: []pdu.Record{{Index: 1, Sender: "10086", Timestamp: "2026-10-15 12:02:00", Text: "new", ConcatRef: -1}}}, st, fakeSIM{}, false, []string{"bark", "telegram"})
	a.SetChannels(nil)
	if err := a.SyncIfStale(0); err != nil {
		t.Fatal(err)
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	msgs, err := st.ListMessages(store.UnknownSIM, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages=%+v err=%v", msgs, err)
	}
}
