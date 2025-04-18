package api

import (
	"fmt"
)

type entityType string

const (
	entityTypeSearchDomain    entityType = "search-domain"
	entityTypeRepository      entityType = "repository"
	entityTypeView            entityType = "view"
	entityTypeGroup           entityType = "group"
	entityTypeIngestToken     entityType = "ingest-token"
	entityTypeParser          entityType = "parser"
	entityTypeAction          entityType = "action"
	entityTypeAlert           entityType = "alert"
	entityTypeFilterAlert     entityType = "filter-alert"
	entityTypeFeatureFlag     entityType = "feature-flag"
	entityTypeScheduledSearch entityType = "scheduled-search"
	entityTypeAggregateAlert  entityType = "aggregate-alert"
	entityTypeUser            entityType = "user"
)

func (e entityType) String() string {
	return string(e)
}

type EntityNotFound struct {
	entityType entityType
	key        string
}

func (e EntityNotFound) EntityType() entityType {
	return e.entityType
}

func (e EntityNotFound) Key() string {
	return e.key
}

func (e EntityNotFound) Error() string {
	return fmt.Sprintf("%s %q not found", e.entityType.String(), e.key)
}

func SearchDomainNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeSearchDomain,
		key:        name,
	}
}

func RepositoryNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeRepository,
		key:        name,
	}
}

func ViewNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeView,
		key:        name,
	}
}

func GroupNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeGroup,
		key:        name,
	}
}

func IngestTokenNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeIngestToken,
		key:        name,
	}
}

func ParserNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeParser,
		key:        name,
	}
}

func ActionNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeAction,
		key:        name,
	}
}

func AlertNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeAlert,
		key:        name,
	}
}

func FilterAlertNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeFilterAlert,
		key:        name,
	}
}

func FeatureFlagNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeFeatureFlag,
		key:        name,
	}
}

func ScheduledSearchNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeScheduledSearch,
		key:        name,
	}
}

func AggregateAlertNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeAggregateAlert,
		key:        name,
	}
}

func UserNotFound(name string) error {
	return EntityNotFound{
		entityType: entityTypeUser,
		key:        name,
	}
}
