package controller

import (
	"time"
)

// HumioFinalizer generic finalizer to add to resources
const HumioFinalizer = "core.humio.com/finalizer"

// CommonConfig has common configuration parameters for all controllers.
type CommonConfig struct {
	RequeuePeriod              time.Duration // How frequently to requeue a resource for reconcile.
	CriticalErrorRequeuePeriod time.Duration // How frequently to requeue a resource for reconcile after a critical error.
}
