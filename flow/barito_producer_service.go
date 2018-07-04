package flow

import (
	"net/http"
	"sync"
	"time"

	"github.com/BaritoLog/go-boilerplate/timekit"
	"github.com/Shopify/sarama"
)

type BaritoProducerService interface {
	Start() error
	Close()
	ServeHTTP(rw http.ResponseWriter, req *http.Request)
}

// TODO: separate leaky bucket
type baritoProducerService struct {
	Address     string
	MaxTps      int
	Producer    sarama.SyncProducer
	TopicSuffix string
	tps         int
	server      *http.Server
	tick        <-chan time.Time
	stop        chan int
	mux         sync.Mutex
}

func NewBaritoProducerService(addr string, producer sarama.SyncProducer, maxTps int, topicSuffix string) BaritoProducerService {
	return &baritoProducerService{
		Address:     addr,
		MaxTps:      maxTps,
		Producer:    producer,
		tps:         maxTps,
		TopicSuffix: topicSuffix,
	}
}

func (a *baritoProducerService) Start() error {
	if a.server == nil {
		a.server = &http.Server{
			Addr:    a.Address,
			Handler: a,
		}
	}

	a.tick = time.Tick(timekit.Duration("1s"))
	a.stop = make(chan int)
	a.tps = a.MaxTps

	go a.loopRefillBucket()

	return a.server.ListenAndServe()
}

func (a *baritoProducerService) Close() {
	if a.server != nil {
		a.server.Close()
	}

	a.Producer.Close()

	go func() {
		a.stop <- 1
	}()

}

func (s *baritoProducerService) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !s.leakBucket() {
		onLimitExceeded(rw)
		return
	}
	timber, err := ConvertRequestToTimber(req)
	if err != nil {
		onBadRequest(rw, err)
		return
	}

	err = kafkaStore(s.Producer, timber, s.TopicSuffix)
	if err != nil {
		onStoreError(rw, err)
		return
	}

	onSuccess(rw)
}

func (a *baritoProducerService) loopRefillBucket() {
	for {
		select {
		case <-a.tick:
			a.refillBucket()
		case <-a.stop:
			return
		}
	}
}

func (a *baritoProducerService) refillBucket() {
	a.tps = a.MaxTps
}

func (a *baritoProducerService) leakBucket() bool {
	a.mux.Lock()
	defer a.mux.Unlock()

	if a.tps <= 1 {
		return false
	}

	a.tps--
	return true
}
