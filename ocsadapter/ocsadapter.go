// nolint:lll
// Generates the ocsadapter adapter's resource yaml. It contains the adapter's configuration, name, supported template
// names (metric in this case), and whether it is session or no-session based.
//go:generate $GOPATH/src/istio.io/istio/bin/mixer_codegen.sh -a mixer/adapter/ocsadapter/config/config.proto -x "-s=false -n ocsadapter -t authorization"

package ocsadapter

import (
	"context"
	"fmt"
	"github.com/ivyanni/ocsadapter/ocsadapter/client"
	"github.com/ivyanni/ocsadapter/ocsadapter/config"
	"github.com/ivyanni/ocsadapter/ocsadapter/redis"
	"net"
	"strconv"
	"strings"

	"google.golang.org/grpc"

	"istio.io/api/mixer/adapter/model/v1beta1"
	policy "istio.io/api/policy/v1beta1"
	"istio.io/istio/mixer/pkg/status"
	"istio.io/istio/mixer/template/authorization"
	"istio.io/pkg/log"
)

type (
	// Server is basic server interface
	Server interface {
		Addr() string
		Close() error
		Run(shutdown chan error)
	}

	// OcsAdapter supports metric template.
	OcsAdapter struct {
		listener net.Listener
		server   *grpc.Server
	}
)

var _ authorization.HandleAuthorizationServiceServer = &OcsAdapter{}

// HandleMetric records metric entries
func (s *OcsAdapter) HandleAuthorization(ctx context.Context, r *authorization.HandleAuthorizationRequest) (*v1beta1.CheckResult, error) {

	log.Infof("received request %v\n", *r)

	cfg := &config.Params{}

	if r.AdapterConfig != nil {
		if err := cfg.Unmarshal(r.AdapterConfig.Value); err != nil {
			log.Errorf("error unmarshalling adapter config: %v", err)
			return nil, err
		}
	}

	decodeValue := func(in interface{}) interface{} {
		switch t := in.(type) {
		case *policy.Value_StringValue:
			return t.StringValue
		case *policy.Value_Int64Value:
			return t.Int64Value
		case *policy.Value_DoubleValue:
			return t.DoubleValue
		default:
			return fmt.Sprintf("%v", in)
		}
	}

	decodeValueMap := func(in map[string]*policy.Value) map[string]interface{} {
		out := make(map[string]interface{}, len(in))
		for k, v := range in {
			out[k] = decodeValue(v.GetValue())
		}
		return out
	}

	log.Infof("ocs address: %v", cfg.OcsAddress)
	log.Infof("request units: %v", cfg.RequestUnits)
	log.Infof("units until update: %v", cfg.UnitsUntilUpdate)

	requestUnits := strings.Split(cfg.RequestUnits, ",")
	unitsUntilUpdate, err := strconv.Atoi(cfg.UnitsUntilUpdate)
	if err != nil {
		log.Fatalf("Couldn't parse value %v", cfg.UnitsUntilUpdate)
	}

	props := decodeValueMap(r.Instance.Subject.Properties)
	log.Infof("%v", props)

	for k, v := range props {
		fmt.Println("k:", k, "v:", v)
		if k == "application_id" {
			value := fmt.Sprintf("%v", v)
			remainingUnits := redis.GetRemainingUnits(value)
			usedUnits := redis.GetUsedUnits(value)
			log.Infof("success!! value = %v", remainingUnits)
			if remainingUnits == 0 {
				grantedUnits := client.GetUnits(cfg.OcsAddress, value, requestUnits, usedUnits)
				log.Infof("success!! granted units = %v", grantedUnits)
				if grantedUnits > 0 {
					redis.SetUsedUnits(value, 1)
					redis.SetRemainingUnits(value, grantedUnits - 1)
					return &v1beta1.CheckResult{
						Status: status.OK,
						ValidUseCount: 1,
					}, nil
				}
			} else {
				if usedUnits == unitsUntilUpdate {
					grantedUnits := client.GetUnits(cfg.OcsAddress, value, requestUnits, usedUnits)
					redis.SetUsedUnits(value, 0)
					redis.SetRemainingUnits(value, grantedUnits)
				}
				redis.IncreaseUsedValue(value)
				redis.DecreaseRemainingValue(value)
				return &v1beta1.CheckResult{
					Status: status.OK,
					ValidUseCount: 1,
				}, nil
			}
		}
	}

	log.Infof("failure; header not provided")
	return &v1beta1.CheckResult{
		Status: status.WithPermissionDenied("Unauthorized..."),
	}, nil
}

// Addr returns the listening address of the server
func (s *OcsAdapter) Addr() string {
	return s.listener.Addr().String()
}

// Run starts the server run
func (s *OcsAdapter) Run(shutdown chan error) {
	shutdown <- s.server.Serve(s.listener)
}

// Close gracefully shuts down the server; used for testing
func (s *OcsAdapter) Close() error {
	usageMap := redis.GetUsageInfo()
	client.Terminate(usageMap)

	if s.server != nil {
		s.server.GracefulStop()
	}

	if s.listener != nil {
		_ = s.listener.Close()
	}

	return nil
}

// NewOcsAdapter creates a new IBP adapter that listens at provided port.
func NewOcsAdapter(addr string) (Server, error) {
	if addr == "" {
		addr = "0"
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%s", addr))
	if err != nil {
		return nil, fmt.Errorf("unable to listen on socket: %v", err)
	}
	s := &OcsAdapter{
		listener: listener,
	}
	fmt.Printf("listening on \"%v\"\n", s.Addr())
	s.server = grpc.NewServer()
	authorization.RegisterHandleAuthorizationServiceServer(s.server, s)
	return s, nil
}
