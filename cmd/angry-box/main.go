package main

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func main() {
	f := factory.New()

	for _, kind := range []model.BackendKind{model.SingBox, model.Xray} {
		b, err := f.Create(kind)
		if err != nil {
			fmt.Printf("error creating %s: %v\n", kind, err)
			continue
		}
		fmt.Printf("backend: %s (version %s)\n", b.Name(), b.Version())
	}
}
