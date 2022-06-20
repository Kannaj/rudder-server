package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"sync"

	"github.com/dgraph-io/badger/v3"
	"github.com/rudderlabs/rudder-server/admin"
	"github.com/rudderlabs/rudder-server/config"
	"github.com/rudderlabs/rudder-server/jobsdb"
	"github.com/rudderlabs/rudder-server/services/archiver"
	"github.com/rudderlabs/rudder-server/services/stats"
	"github.com/rudderlabs/rudder-server/utils/logger"
	"github.com/valyala/fasthttp"
)

var (
	addr     = flag.String("addr", ":1500", "TCP address to listen to")
	compress = flag.Bool("compress", false, "Whether to enable transparent response compression")
)

func initJobsDB() {
	config.Load()
	logger.Init()
	admin.Init()
	jobsdb.Init()
	jobsdb.Init2()
	jobsdb.Init3()

	stats.Setup()
	archiver.Init()
}

func main() {
	flag.Parse()

	initJobsDB()
	// db := jobsdb.NewForWrite("test_ingest", jobsdb.WithClearDB(true))
	// defer db.Close()

	// db.Start()
	// defer db.Stop()

	lb := &StoreBalancer{}

	for i := 0; i < 2; i++ {

		b, err := badger.Open(badger.DefaultOptions(fmt.Sprintf("./db-%d", i)))
		if err != nil {
			log.Fatal(err)
		}
		db := &badgerStore{
			db: b,
		}

		jb := &JobBuffer{db: db}

		go func() {
			for {
				jb.Flush()
			}
		}()

		lb.Stores = append(lb.Stores, jb)
	}

	h := IngestHandler{store: lb}

	if err := fasthttp.ListenAndServe(*addr, h.HandleFastHTTP); err != nil {
		log.Fatalf("Error in ListenAndServe: %v", err)
	}
}

type IngestHandler struct {
	store Storer
}

func (h *IngestHandler) HandleFastHTTP(ctx *fasthttp.RequestCtx) {
	fmt.Fprintf(ctx, "OK\n\n")

	ctx.SetContentType("text/plain; charset=utf8")

	body := ctx.PostBody()

	err := h.store.Store([]*jobsdb.JobT{{
		WorkspaceId:  "test_ingest",
		EventPayload: body,
		PayloadSize:  int64(len(body)),
		Parameters:   []byte("{\"event\":\"test\"}"),
	}})
	if err != nil {
		log.Println(err)
	}

}

type Storer interface {
	Store(jobs []*jobsdb.JobT) error
}

type StoreBalancer struct {
	Stores []Storer
}

func (s *StoreBalancer) Store(jobs []*jobsdb.JobT) error {

	// key := jobs[0].WorkspaceId + jobs[0].UserID

	// x := xxhash.Sum64String(key)
	i := rand.Intn(len(s.Stores))

	return s.Stores[i].Store(jobs)
}

type JobWithACK struct {
	job *jobsdb.JobT
	ack chan error
}

type JobBuffer struct {
	mu   sync.Mutex
	db   Storer
	jobs []JobWithACK
}

func (jb *JobBuffer) Store(jobs []*jobsdb.JobT) error {
	jb.mu.Lock()
	for _, j := range jobs {
		jb.jobs = append(jb.jobs, JobWithACK{
			j,
			make(chan error, 1),
		})
	}
	a := jb.jobs[len(jb.jobs)-1].ack
	jb.mu.Unlock()
	return <-a
}

func (jb *JobBuffer) Flush() {
	jb.mu.Lock()
	acks := make([]chan error, len(jb.jobs))
	jobs := make([]*jobsdb.JobT, len(jb.jobs))
	for i, j := range jb.jobs {
		jobs[i] = j.job
		acks[i] = j.ack
	}
	jb.jobs = nil

	jb.mu.Unlock()

	err := jb.db.Store(jobs)
	for _, a := range acks {
		a <- err
		close(a)
	}

}

type badgerStore struct {
	db *badger.DB
}

func (b *badgerStore) Store(jobs []*jobsdb.JobT) error {
	txn := b.db.NewTransaction(true)

	seq, err := b.db.GetSequence([]byte("foo"), 1000)
	if err != nil {
		return err
	}
	defer seq.Release()
	for _, j := range jobs {
		num, err := seq.Next()
		if err != nil {
			return err
		}

		key := []byte(strconv.FormatInt(int64(num), 36))
		value := []byte(j.EventPayload)
		if err := txn.Set(key, value); err == badger.ErrTxnTooBig {
			_ = txn.Commit()
			txn = b.db.NewTransaction(true)
			_ = txn.Set(key, value)

		} else if err != nil {
			return err
		}
	}
	return txn.Commit()

}
