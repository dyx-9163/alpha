package mysql

import (
	"aifar-deployment/backend/internal/apps/mysqlbundle"
	"aifar-deployment/backend/internal/store"
)

type Bundle = mysqlbundle.Bundle

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	return mysqlbundle.SelectBundle(resources, version)
}

func VerifyBundle(bundle Bundle) error {
	return mysqlbundle.VerifyBundle(bundle)
}
