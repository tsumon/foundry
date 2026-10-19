package model

type Secrets interface {
	Set(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}
