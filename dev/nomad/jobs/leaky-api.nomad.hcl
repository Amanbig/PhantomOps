# Leaks memory past its limit -> OOM kill -> restarts -> allocation failure.
job "leaky-api" {
  datacenters = ["dc1"]
  type        = "service"

  group "api" {
    restart {
      attempts = 2
      interval = "5m"
      delay    = "2s"
      mode     = "fail"
    }
    reschedule {
      attempts  = 0
      unlimited = false
    }

    task "api" {
      driver = "docker"
      config {
        image   = "python:3.12-alpine"
        command = "python"
        args = ["-u", "-c", "import time\nhog=[]\nwhile True:\n    hog.append(bytearray(4*1024*1024))\n    print('ERROR: database connection pool exhausted', flush=True)\n    time.sleep(1)\n"]
      }
      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
