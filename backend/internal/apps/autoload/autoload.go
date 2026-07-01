package autoload

import (
	_ "aifar-deployment/backend/internal/apps/aifar"
	_ "aifar-deployment/backend/internal/apps/docker"
	_ "aifar-deployment/backend/internal/apps/minio"
	_ "aifar-deployment/backend/internal/apps/mysql"
	_ "aifar-deployment/backend/internal/apps/mysqlrouter"
	_ "aifar-deployment/backend/internal/apps/nacos"
	_ "aifar-deployment/backend/internal/apps/redis"
)
