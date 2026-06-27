package keyring

import "github.com/zalando/go-keyring"

type Store struct {
	service string
	user    string
}

func New(service, user string) *Store {
	return &Store{service: service, user: user}
}

func (s *Store) Get() (string, error) {
	return keyring.Get(s.service, s.user)
}

func (s *Store) Set(value string) error {
	return keyring.Set(s.service, s.user, value)
}

func (s *Store) Delete() error {
	return keyring.Delete(s.service, s.user)
}

// Delete is a package-level convenience for cleanup in tests.
func Delete(service, user string) error {
	return keyring.Delete(service, user)
}