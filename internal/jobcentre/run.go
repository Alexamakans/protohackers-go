package jobcentre

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Alexamakans/slib/pkg/tkn"
)

func Run() {
	bindAddress, err := net.ResolveTCPAddr("tcp", "0.0.0.0:17777")
	if err != nil {
		panic(err)
	}
	listener, err := net.ListenTCP("tcp", bindAddress)
	if err != nil {
		panic(err)
	}
	log.Println("Listening on 0.0.0.0:17777")
	server := NewServer()
	server.Start(listener)
}

type Method string

const (
	MethodPut    = "put"
	MethodGet    = "get"
	MethodDelete = "delete"
	MethodAbort  = "abort"
)

type Status string

const (
	StatusOk    = "ok"
	StatusError = "error"
	StatusNoJob = "no-job"
)

// PutRequest puts a job into a given queue.
type PutRequest struct {
	Queue    string         `json:"queue"`
	Job      map[string]any `json:"job"`
	Priority uint           `json:"pri"`
}

type PutResponse struct {
	Status Status `json:"status"`
	Id     uint   `json:"id"`
}

// GetRequest gets and removes the highest priority job found in the given queues.
type GetRequest struct {
	Queues []string `json:"queues"`
	Wait   *bool    `json:"wait,omitempty"`
}

type GetResponse struct {
	Status   Status          `json:"status"`
	Id       *uint           `json:"id,omitempty"`
	Job      *map[string]any `json:"job,omitempty"`
	Priority *uint           `json:"pri,omitempty"`
	Queue    *string         `json:"queue,omitempty"`
}

// DeleteRequest deletes the job with the given id.
// Valid from any client.
// If the job is not found, respond with StatusNoJob.
type DeleteRequest struct {
	Id uint `json:"id"`
}

type DeleteResponse struct {
	Status Status `json:"status"`
}

// AbortRequest puts the job with the given id back in its queue.
// Only valid from the client that is currently working on that job.
//
// It is an error to try to abort a job that you are not currently working on.
// If the job has not been assigned or it has been deleted, respond with StatusNoJob.
type AbortRequest struct {
	Method Method `json:"request"`
	Id     uint   `json:"id"`
}

type AbortResponse struct {
	Status Status `json:"status"`
}

type Job struct {
	owner    uint
	queue    string
	id       uint
	jobData  map[string]any
	priority uint
}

type Queue struct {
	name     string
	jobsById map[uint]*Job
}

type Server struct {
	queuesByName map[string]*Queue
	jobsById     map[uint]*Job

	nextJobId    uint
	nextClientId uint

	m sync.Mutex
}

func NewServer() *Server {
	return &Server{
		queuesByName: make(map[string]*Queue),
		jobsById:     make(map[uint]*Job),

		nextJobId:    1,
		nextClientId: 1,
	}
}

func (o *Server) Start(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		log.Printf("Accepted connection %s", conn.RemoteAddr())

		go o.HandleClient(o.nextClientId, conn)
		o.nextClientId++
	}
}

func (o *Server) HandleClient(clientId uint, conn net.Conn) {
	defer conn.Close()
	defer o.cleanupClient(clientId)
	tokenizer := tkn.NewDelimitedTokenizer(10_000, "\n", conn)
	requests := tokenizer.Stream()
	for request := range requests {
		var obj map[string]any
		if err := json.Unmarshal([]byte(request), &obj); err != nil {
			o.errorString(conn, "bad json")
			continue
		}
		var methodAny any
		var ok bool
		if methodAny, ok = obj["request"]; !ok {
			o.errorString(conn, "no 'request' property")
			continue
		}
		method, ok := methodAny.(string)
		if !ok {
			o.errorString(conn, fmt.Sprintf("invalid 'request', wrong type: %#v", methodAny))
			continue
		}
		switch method {
		case "put":
			var req PutRequest
			if err := json.Unmarshal([]byte(request), &req); err != nil {
				o.errorString(conn, "failed parsing as PutRequest")
				continue
			}
			res := o.HandlePutRequest(clientId, req)
			if err := o.send(conn, res); err != nil {
				log.Printf("failed sending PutResponse: %#v", res)
			}
		case "get":
			var req GetRequest
			if err := json.Unmarshal([]byte(request), &req); err != nil {
				o.errorString(conn, "failed parsing as GetRequest")
				continue
			}
			res := o.HandleGetRequest(clientId, req)
			if err := o.send(conn, res); err != nil {
				log.Printf("failed sending GetResponse: %#v", res)
			}
		case "delete":
			var req DeleteRequest
			if err := json.Unmarshal([]byte(request), &req); err != nil {
				o.errorString(conn, "failed parsing as DeleteRequest")
				continue
			}
			res := o.HandleDeleteRequest(clientId, req)
			if err := o.send(conn, res); err != nil {
				log.Printf("failed sending DeleteResponse: %#v", res)
			}
		case "abort":
			var req AbortRequest
			if err := json.Unmarshal([]byte(request), &req); err != nil {
				o.errorString(conn, "failed parsing as AbortRequest")
				continue
			}
			res := o.HandleAbortRequest(clientId, req)
			if err := o.send(conn, res); err != nil {
				log.Printf("failed sending AbortResponse: %#v", res)
			}
		default:
			o.errorString(conn, fmt.Sprintf("invalid 'request' value: %#v", method))
		}
	}
}

func (o *Server) cleanupClient(clientId uint) {
	o.m.Lock()
	defer o.m.Unlock()
	for _, job := range o.jobsById {
		if job.owner == clientId {
			o.queuesByName[job.queue].jobsById[job.id] = job
		}
	}
}

func (o *Server) send(conn net.Conn, v any) error {
	var data []byte
	var err error
	if data, err = json.Marshal(&v); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}

func (o *Server) errorString(conn net.Conn, errorMsg string) {
	log.Printf("sending error: %q", errorMsg)
	if _, err := fmt.Fprintf(conn, `{"status":"error","error":"%s"}%s`, errorMsg, "\n"); err != nil {
		log.Printf("failed sending error: %+v", err)
	}
}

func (o *Server) NewQueue(name string) *Queue {
	q := &Queue{
		name:     name,
		jobsById: make(map[uint]*Job),
	}
	o.queuesByName[name] = q
	return q
}

func (o *Server) HandlePutRequest(clientId uint, req PutRequest) PutResponse {
	log.Printf("%06d: %#v", clientId, req)
	o.m.Lock()
	defer o.m.Unlock()
	id := o.nextJobId
	var queue *Queue
	var ok bool
	if queue, ok = o.queuesByName[req.Queue]; !ok {
		queue = o.NewQueue(req.Queue)
		o.queuesByName[req.Queue] = queue
	}
	job := &Job{
		queue:    req.Queue,
		id:       id,
		jobData:  req.Job,
		priority: req.Priority,
	}
	queue.jobsById[id] = job
	o.jobsById[id] = job
	o.nextJobId++

	return PutResponse{
		Status: StatusOk,
		Id:     id,
	}
}

func (o *Server) HandleGetRequest(clientId uint, req GetRequest) GetResponse {
	log.Printf("%06d: %#v", clientId, req)
	o.m.Lock()
	defer o.m.Unlock()
	var job *Job
	var highestPriorityJobQueue string
	for {
		var highestPriority uint
		var highestPriorityJob *Job
		for _, queue := range req.Queues {
			if _, ok := o.queuesByName[queue]; !ok {
				continue
			}
			for _, job := range o.queuesByName[queue].jobsById {
				if job.priority >= highestPriority {
					highestPriority = job.priority
					highestPriorityJob = job
					highestPriorityJobQueue = queue
				}
			}
		}

		job = highestPriorityJob
		if job != nil || req.Wait == nil || !*req.Wait {
			break
		}
		o.m.Unlock()
		time.Sleep(500 * time.Millisecond)
		o.m.Lock()
	}

	if job == nil {
		return GetResponse{Status: StatusNoJob}
	}

	job.owner = clientId

	delete(o.queuesByName[highestPriorityJobQueue].jobsById, job.id)

	return GetResponse{
		Status:   StatusOk,
		Id:       &job.id,
		Job:      &job.jobData,
		Priority: &job.priority,
		Queue:    &highestPriorityJobQueue,
	}
}

func (o *Server) HandleDeleteRequest(clientId uint, req DeleteRequest) DeleteResponse {
	log.Printf("%06d: %#v", clientId, req)
	o.m.Lock()
	defer o.m.Unlock()
	job, exists := o.jobsById[req.Id]
	if !exists {
		return DeleteResponse{Status: StatusNoJob}
	}

	delete(o.jobsById, job.id)
	for _, queue := range o.queuesByName {
		delete(queue.jobsById, job.id)
	}

	return DeleteResponse{
		Status: StatusOk,
	}
}

func (o *Server) HandleAbortRequest(clientId uint, req AbortRequest) AbortResponse {
	log.Printf("%06d: %#v", clientId, req)
	o.m.Lock()
	defer o.m.Unlock()

	job, exists := o.jobsById[req.Id]
	if !exists {
		log.Printf("couldn't find job %d", req.Id)
		return AbortResponse{Status: StatusNoJob}
	}

	if job.owner == 0 {
		log.Printf("job %d wasn't active", req.Id)
		return AbortResponse{Status: StatusNoJob}
	}

	if job.owner != clientId {
		log.Printf("%d tried to abort job id %d, but the job is owned by %d", clientId, job.id, job.owner)
		return AbortResponse{Status: StatusError}
	}

	o.queuesByName[job.queue].jobsById[job.id] = job

	return AbortResponse{Status: StatusOk}
}
