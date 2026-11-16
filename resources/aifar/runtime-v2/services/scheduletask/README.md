# Scheduled Tasks runtime module

This Java 21 module uses application name `alpha-scheduletask` and HTTP port
`38009`. Its Dockerfile follows the existing AIFAR Java module convention.

Before packaging or installing, place exactly one executable Spring Boot JAR
in `target/`. The source module is
`alpha-scheduletask/alpha-scheduletask-server` in `alpha-java-cloud`.
No JAR is included by this configuration change.

Use the same offline Java 21 base image as the other Java services. Rescan
resources after delivering the updated bundle, then install the new service
through the existing service installation flow.

Verify the Nacos configuration and actual HTTP port, the readiness endpoint,
database and Redis connectivity, and XXL-JOB executor connectivity before
acceptance. The HTTP service descriptor does not configure an additional
XXL-JOB executor port. Start with one replica until scheduling behavior has
been verified in the target environment.
