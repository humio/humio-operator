package controller

import (
	"github.com/humio/humio-operator/pkg/controller/humiocluster"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, humiocluster.Add)
}
