# Image does not exist -> driver failure.
job "bad-image" {
  datacenters = ["dc1"]
  type        = "service"

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
        image = "registry.invalid/phantom/does-not-exist:v9"
      }
      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
