module code.cloudfoundry.org/quarks-statefulset

require (
	code.cloudfoundry.org/quarks-utils v0.0.3-0.20210303091853-3b41f4b87e33
	github.com/elazarl/goproxy v0.0.0-20191011121108-aa519ddbe484 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/go-sql-driver/mysql v1.4.1 // indirect
	github.com/jmoiron/sqlx v1.2.0 // indirect
	github.com/lib/pq v1.2.0 // indirect
	github.com/mattn/go-sqlite3 v1.11.0 // indirect
	github.com/mitchellh/mapstructure v1.3.2 // indirect
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.14.0
	github.com/pkg/errors v0.9.1
	github.com/spf13/afero v1.4.1
	github.com/spf13/cobra v1.1.1
	github.com/spf13/viper v1.7.0
	go.uber.org/zap v1.18.1
	gomodules.xyz/jsonpatch/v2 v2.2.0
	k8s.io/api v0.21.3
	k8s.io/apiextensions-apiserver v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.6
	sigs.k8s.io/yaml v1.2.0
)

go 1.15
