# Installer Template Overrides
# 安装脚本覆盖模板

Put optional shell template overrides in this directory when a packaged
environment needs to tune installer scripts without rebuilding AIFAR.

生产环境需要在不重新编译 AIFAR 的情况下微调安装脚本时，可以把覆盖模板放到这里。

Expected paths:

- `docker/install.sh`
- `docker/uninstall.sh`
- `mysql/standalone/install.sh`
- `mysql/standalone/uninstall.sh`
- `mysql/innodb-cluster/bootstrap.sh`
- `mysql-router/install.sh`
- `mysql-router/uninstall.sh`
- `redis/standalone/install.sh`
- `redis/standalone/uninstall.sh`
- `redis/sentinel/configure-node.sh`
- `redis/sentinel/uninstall-node.sh`
- `redis/cluster/enable-node.sh`
- `redis/cluster/bootstrap.sh`
- `minio/standalone/install.sh`
- `minio/standalone/uninstall.sh`
- `minio/distributed/configure-node.sh`

If a file is absent, AIFAR uses the built-in template embedded in the binary.
Keep templates compatible with the variables used by the built-in script for
the same path.

如果对应文件不存在，AIFAR 会使用二进制内置模板。覆盖模板需要保持和同路径内置脚本相同的模板变量约定。
