package mysqlrouter

import (
	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/store"
)

type Bundle = mysqlapp.Bundle

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	return mysqlapp.SelectBundle(resources, version)
}

func VerifyBundle(bundle Bundle) error {
	return mysqlapp.VerifyBundle(bundle)
}
