FROM bellsoft/liberica-openjre-rocky:21
LABEL maintainer=aifar-team

WORKDIR /data/aifarsoft/javaApi/aifar-scheduletask

COPY target/*.jar ./aifar-scheduletask.jar

EXPOSE 38009

ENTRYPOINT ["java","--add-opens=java.base/java.lang=ALL-UNNAMED","--add-opens=java.base/java.lang.reflect=ALL-UNNAMED","--add-opens=java.base/java.lang.invoke=ALL-UNNAMED","--add-opens=java.base/java.math=ALL-UNNAMED","--add-opens=java.base/sun.net.util=ALL-UNNAMED","--add-opens=java.base/java.io=ALL-UNNAMED","--add-opens=java.base/java.net=ALL-UNNAMED","--add-opens=java.base/java.nio=ALL-UNNAMED","--add-opens=java.base/java.security=ALL-UNNAMED","--add-opens=java.base/java.text=ALL-UNNAMED","--add-opens=java.base/java.time=ALL-UNNAMED","--add-opens=java.base/java.util=ALL-UNNAMED","--add-opens=java.base/jdk.internal.module=ALL-UNNAMED","--add-opens=java.base/sun.security.util=ALL-UNNAMED","-Dfile.encoding=utf8","-Djava.security.egd=file:/dev/./urandom","-jar","aifar-scheduletask.jar"]
