package contracts

import "fmt"

type Contracts struct {
	registry map[string]any
}

func New() *Contracts {
	return &Contracts{registry: map[string]any{}}
}

func (c *Contracts) Register(key string, contract any) {
	if _, ok := c.registry[key]; ok {
		panic(fmt.Sprintf("contracts: %q is already registered", key))
	}
	c.registry[key] = contract
}

func (c *Contracts) Get(key string) (any, bool) {
	v, ok := c.registry[key]
	return v, ok
}
