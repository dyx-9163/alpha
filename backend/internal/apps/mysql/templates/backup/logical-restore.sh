#!/usr/bin/env bash
set -euo pipefail

exec python3 - "{{.WorkDir}}" "{{.DumpDir}}" "{{.MySQLShell}}" "{{.Threads}}" <<'PY'
import os
import stat
import subprocess
import sys

work_dir, dump_dir, mysqlsh, threads = sys.argv[1:]

def fail():
    print("controlled restore path validation failed", file=sys.stderr)
    raise SystemExit(1)

def open_dir(path, exact_mode=None):
    if not path.startswith("/"):
        fail()
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
    try:
        for component in [part for part in path.split("/") if part]:
            next_fd = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = next_fd
        details = os.fstat(fd)
        if not stat.S_ISDIR(details.st_mode) or details.st_uid != os.geteuid():
            fail()
        if exact_mode is not None and stat.S_IMODE(details.st_mode) != exact_mode:
            fail()
        if exact_mode is None and details.st_mode & 0o022:
            fail()
        return fd
    except Exception:
        try: os.close(fd)
        except OSError: pass
        fail()

def open_file(parent_fd, name, exact_mode=None):
    try:
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent_fd)
        details = os.fstat(fd)
        if not stat.S_ISREG(details.st_mode) or details.st_uid != os.geteuid():
            os.close(fd)
            fail()
        if exact_mode is not None and stat.S_IMODE(details.st_mode) != exact_mode:
            os.close(fd)
            fail()
        if exact_mode is None and details.st_mode & 0o022:
            os.close(fd)
            fail()
        return fd
    except Exception:
        fail()

work_fd = open_dir(work_dir, 0o700)
dump_fd = open_dir(dump_dir, 0o700)
secret_fd = open_file(work_fd, "secret-context.cnf", 0o600)
mysqlsh_fd = open_file(open_dir(os.path.dirname(mysqlsh)), os.path.basename(mysqlsh))
js = '''util.loadDump("/proc/self/fd/%d", {
  threads: {{.Threads}},
  loadUsers: false,
  ignoreExistingObjects: false,
  skipBinlog: false,
  showProgress: false
});
''' % dump_fd
try:
    js_fd = os.open("logical-restore.js", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=work_fd)
    os.write(js_fd, js.encode("utf-8"))
    os.fsync(js_fd)
    js_details = os.fstat(js_fd)
    if not stat.S_ISREG(js_details.st_mode) or js_details.st_uid != os.geteuid() or stat.S_IMODE(js_details.st_mode) != 0o600:
        fail()
    completed = subprocess.run([
        "/proc/self/fd/%d" % mysqlsh_fd,
        "--defaults-file=/proc/self/fd/%d" % secret_fd,
        "--js",
        "--file", "/proc/self/fd/%d" % js_fd,
    ], pass_fds=(dump_fd, secret_fd, mysqlsh_fd, js_fd), check=False)
    raise SystemExit(completed.returncode)
finally:
    for fd in (work_fd, dump_fd, secret_fd, mysqlsh_fd):
        try: os.close(fd)
        except OSError: pass
PY
