package prelaunch

import (
	"context"
	"sync"
	"time"
)

type memoryRateKey struct {
	key    string
	window time.Time
}

type MemoryRepository struct {
	mu          sync.Mutex
	requests    map[string]Submission
	emailHashes map[string]string
	rates       map[memoryRateKey]int
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		requests:    make(map[string]Submission),
		emailHashes: make(map[string]string),
		rates:       make(map[memoryRateKey]int),
	}
}

func (repository *MemoryRepository) Allow(
	_ context.Context,
	key string,
	window time.Time,
	limit int,
) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	rateKey := memoryRateKey{key: key, window: window}
	repository.rates[rateKey]++
	return repository.rates[rateKey] <= limit, nil
}

func (repository *MemoryRepository) Submit(
	_ context.Context,
	submission Submission,
) (SubmitResult, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if existingID, exists := repository.emailHashes[submission.EmailHash]; exists {
		return SubmitResult{RequestID: existingID, Created: false}, nil
	}
	repository.requests[submission.ID] = submission
	repository.emailHashes[submission.EmailHash] = submission.ID
	return SubmitResult{RequestID: submission.ID, Created: true}, nil
}

func (repository *MemoryRepository) Requests() []Submission {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	requests := make([]Submission, 0, len(repository.requests))
	for _, request := range repository.requests {
		requests = append(requests, request)
	}
	return requests
}
