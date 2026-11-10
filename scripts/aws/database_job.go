package main

// The explicit provisioning Job uses secret-backed libpq inputs, not credentials in argv.
const databaseProvisionJob = `{
  "apiVersion": "batch/v1",
  "kind": "Job",
  "metadata": {
    "name": "rds-provision",
    "namespace": "foc"
  },
  "spec": {
    "backoffLimit": 0,
    "activeDeadlineSeconds": 300,
    "template": {
      "metadata": {
        "labels": {
          "app": "rds-provision"
        },
        "annotations": {
          "sidecar.istio.io/nativeSidecar": "true"
        }
      },
      "spec": {
        "serviceAccountName": "rds-provision",
        "restartPolicy": "Never",
        "containers": [
          {
            "name": "provision",
            "image": "postgres:18.1",
            "command": [
              "psql",
              "-X",
              "--set",
              "ON_ERROR_STOP=1",
              "-f",
              "/setup/setup.sql"
            ],
            "env": [
              {
                "name": "PGHOST",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGHOST"
                  }
                }
              },
              {
                "name": "PGPORT",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGPORT"
                  }
                }
              },
              {
                "name": "PGDATABASE",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGDATABASE"
                  }
                }
              },
              {
                "name": "PGUSER",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGUSER"
                  }
                }
              },
              {
                "name": "PGPASSWORD",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGPASSWORD"
                  }
                }
              },
              {
                "name": "PGSSLMODE",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGSSLMODE"
                  }
                }
              },
              {
                "name": "PGSSLROOTCERT",
                "valueFrom": {
                  "secretKeyRef": {
                    "name": "rds-provision-admin",
                    "key": "PGSSLROOTCERT"
                  }
                }
              }
            ],
            "securityContext": {
              "runAsNonRoot": true,
              "runAsUser": 65532,
              "allowPrivilegeEscalation": false,
              "capabilities": {
                "drop": [
                  "ALL"
                ]
              }
            },
            "volumeMounts": [
              {
                "name": "setup",
                "mountPath": "/setup",
                "readOnly": true
              },
              {
                "name": "rds-ca",
                "mountPath": "/var/run/foc/rds",
                "readOnly": true
              }
            ],
            "resources": {
              "requests": {
                "cpu": "50m",
                "memory": "64Mi"
              },
              "limits": {
                "memory": "256Mi"
              }
            }
          }
        ],
        "volumes": [
          {
            "name": "setup",
            "secret": {
              "secretName": "rds-provision-admin",
              "items": [
                {
                  "key": "setup.sql",
                  "path": "setup.sql"
                }
              ]
            }
          },
          {
            "name": "rds-ca",
            "configMap": {
              "name": "rds-ca"
            }
          }
        ]
      }
    }
  }
}`
