package portmapper

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/coreos/etcd/client"
	"golang.org/x/net/context"
)

var (
	// RegistryPath sets the location in etcd where portmapper will store data.
	// Default: /opsee.co/portmapper
	RegistryPath = "/opsee.co/portmapper"

	// max retries for exponential backoff
	MaxRetries                      = 11
	RequestTimeoutSec time.Duration = 5
	ETCD_HOST                       = os.Getenv("ETCD_HOST")

	// etcd client config
	cfg = client.Config{
		Endpoints: []string{ETCD_HOST},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}
)

// Service is a mapping between a service name and port. It may also contain
// the hostname where the service is running or the container ID in the
// Hostname field. It will attempt to get this from the HOSTNAME environment
// variable.
type Service struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Hostname string `json:"hostname,omitempty"`
}

// ensure service name has field and valid port
func (s *Service) validate() error {
	if s.Name == "" {
		return fmt.Errorf("Service lacks Name field: %v", s)
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("Service Port is outside valid range: %v", s)
	}

	return nil
}

// returns the complete path of the service in etcd
func (s *Service) path() string {
	return fmt.Sprintf("%s/%s:%d", RegistryPath, s.Name, s.Port)
}

// Marshal a service object to a byte array.
func (s *Service) Marshal() ([]byte, error) {
	bytes, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	return bytes, nil
}

// UnmarshalService deserializes a Service object from a byte array.
func UnmarshalService(bytes []byte) (*Service, error) {
	s := &Service{}
	err := json.Unmarshal(bytes, s)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// Unregister a (service, port) tuple.
func Unregister(name string, port int) error {
	// service doesn't have a name or has an invalid port
	svc := &Service{name, port, os.Getenv("HOSTNAME")}
	if err := svc.validate(); err != nil {
		log.WithFields(log.Fields{
			"action":  "Validate",
			"service": name,
			"port":    svc.Port,
			"errstr":  err.Error(),
		}).Error("Service Validation Failed.")
		panic(err)
	}

	// initialize a new etcd client
	c, err := client.New(cfg)
	if err != nil {
		log.WithFields(log.Fields{"service": "portmapper", "errstr": err.Error()}).Fatal("Error initializing etcd client")
		panic(err)
	}

	kAPI := client.NewKeysAPI(c)

	// attempt to delete the svc's path with exponential backoff
	for try := 0; try < MaxRetries; try++ {
		// 5 second context
		ctx, cancel := context.WithTimeout(context.Background(), RequestTimeoutSec*time.Second)
		defer cancel()

		_, err = kAPI.Delete(ctx, svc.path(), nil)
		if err != nil {
			// handle error
			if err == context.DeadlineExceeded {
				log.WithFields(log.Fields{
					"action":  "Validate",
					"service": name,
					"port":    svc.Port,
					"attempt": try,
					"errstr":  err.Error(),
				}).Warn("Service path deletion exceeded context deadline. Retrying")
			} else {
				log.WithFields(log.Fields{
					"action":  "Validate",
					"service": name,
					"port":    svc.Port,
					"errstr":  err.Error(),
				}).Error("Service path deletion failed.")
				return err
			}
		} else {
			log.WithFields(log.Fields{
				"action":  "set",
				"service": name,
				"port":    svc.Port,
				"path":    svc.path(),
			}).Info("Successfully unregistered service with etcd")
			break
		}

		time.Sleep(2 << uint(try) * time.Millisecond)
	}

	return nil
}

// Register a service with etcd
func Register(name string, port int) error {
	svc := &Service{name, port, os.Getenv("HOSTNAME")}

	if err := svc.validate(); err != nil {
		log.WithFields(log.Fields{
			"action":  "Validate",
			"service": name,
			"port":    svc.Port,
			"errstr":  err.Error(),
		}).Error("Service Validation Failed.")
		panic(err)
	}

	bytes, err := svc.Marshal()
	if err != nil {
		log.WithFields(log.Fields{
			"action":  "Marshall",
			"service": name,
			"port":    svc.Port,
			"errstr":  err.Error(),
		}).Error("Marshalling Failed.")
		panic(err)
	}

	// initialize a new etcd client
	c, err := client.New(cfg)
	if err != nil {
		log.WithFields(log.Fields{"service": "portmapper", "errstr": err.Error()}).Fatal("Error initializing etcd client")
		panic(err)
	}

	kAPI := client.NewKeysAPI(c)

	// attempt to delete the svc's path with exponential backoff
	for try := 0; try < MaxRetries; try++ {
		// 5 second context
		ctx, cancel := context.WithTimeout(context.Background(), RequestTimeoutSec*time.Second)
		defer cancel()

		_, err := kAPI.Set(ctx, svc.path(), string(bytes), nil)
		if err != nil {
			// handle error
			if err == context.DeadlineExceeded {
				log.WithFields(log.Fields{
					"action":  "Register",
					"service": name,
					"port":    svc.Port,
					"attempt": try,
					"errstr":  err.Error(),
				}).Warn("Service registration exceeded context deadline. Retrying")
			} else {
				log.WithFields(log.Fields{
					"action":  "Register",
					"service": name,
					"port":    svc.Port,
					"errstr":  err.Error(),
				}).Error("Service registration failed.")
				return err
			}
		} else {
			log.WithFields(log.Fields{
				"action":  "set",
				"service": name,
				"port":    svc.Port,
				"path":    svc.path(),
			}).Info("Successfully registered service with etcd")
			break
		}

		time.Sleep(2 << uint(try) * time.Millisecond)
	}

	return nil
}

// Services returns an array of Service pointers detailing the service name and
// port of each registered service. (from etcd)
func Services() ([]*Service, error) {
	// initialize a new etcd client
	c, err := client.New(cfg)
	if err != nil {
		log.WithFields(log.Fields{"service": "portmapper", "errstr": err.Error()}).Fatal("Error initializing etcd client")
		panic(err)
	}

	kAPI := client.NewKeysAPI(c)
	services := make([]*Service, 0)

	// attempt to delete the svc's path with exponential backoff
	for try := 0; try < MaxRetries; try++ {
		// 5 second context
		ctx, cancel := context.WithTimeout(context.Background(), RequestTimeoutSec*time.Second)
		defer cancel()

		resp, err := kAPI.Get(ctx, RegistryPath, nil)
		if err != nil {
			// handle error
			if err == context.DeadlineExceeded {
				log.WithFields(log.Fields{
					"action":  "Enumerate Services",
					"attempt": try,
					"errstr":  err.Error(),
				}).Warn("Service enumeration exceeded context deadline. Retrying")
			} else {
				log.WithFields(log.Fields{
					"action":  "Enumerate Services",
					"attempt": try,
					"errstr":  err.Error(),
				}).Error("Service enumeration failed")
				return nil, err
			}
		} else {

			svcNodes := resp.Node.Nodes
			services = make([]*Service, len(svcNodes))

			for i, node := range svcNodes {
				svcStr := node.Value
				svc, err := UnmarshalService([]byte(svcStr))
				if err != nil {
					return nil, err
				}

				services[i] = svc
			}
		}

		time.Sleep(2 << uint(try) * time.Millisecond)
	}
	return services, nil
}
