# Version 2 of job "web" for exercising rollbacks (v1 is healthy, v2 leaks memory).
#   docker exec phantom-nomad nomad job run -detach /jobs/rollback-demo-v1.nomad.hcl
#   (wait until the deployment is successful, then)
#   docker exec phantom-nomad nomad job run -detach /jobs/rollback-demo-v2.nomad.hcl
job "web" {
  datacenters = ["dc1"]
  type        = "service"

  update {
    max_parallel      = 1
    min_healthy_time  = "5s"
    healthy_deadline  = "1m"
    progress_deadline = "2m"
    auto_revert       = false
  }

  group "web" {
    restart {
      attempts = 1
      interval = "5m"
      delay    = "2s"
      mode     = "fail"
    }
    reschedule {
      attempts  = 0
      unlimited = false
    }

    task "web" {
      driver = "docker"
      config {
        image   = "python:3.12-alpine"
        command = "python"
        args    = ["-u", "-c", "import time\nhog=[]\nwhile True:\n    hog.append(bytearray(4*1024*1024))\n    print(\"ERROR: connection pool exhausted\", flush=True)\n    time.sleep(1)\n"]
      }
      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
