package controller

import "time"

// CommonConfig has common configuration parameters for all controllers.
type CommonConfig struct {
	RequeuePeriod time.Duration // How frequently to requeue a resource for reconcile.
}
