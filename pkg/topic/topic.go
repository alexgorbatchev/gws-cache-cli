package topic

import (
	"fmt"

	"gws-cache/pkg/store"
)

type Service struct {
	store *store.DB
}

func NewService(s *store.DB) *Service {
	return &Service{store: s}
}

func (s *Service) RegisterTopic(slug, displayName, query string) (*store.Topic, error) {
	if displayName == "" {
		displayName = slug
	}

	t, err := s.store.CreateTopic(slug, displayName, query)
	if err != nil {
		return nil, fmt.Errorf("creating topic in store: %w", err)
	}

	return t, nil
}

func (s *Service) ListTopics() ([]store.Topic, error) {
	return s.store.ListTopics()
}

func (s *Service) DeleteTopic(slug string) error {
	return s.store.DeleteTopic(slug)
}

func (s *Service) GetTopic(slug string) (*store.Topic, error) {
	return s.store.GetTopicBySlug(slug)
}
