package client

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/fiorix/go-diameter/v4/diam/sm/smpeer"
	"istio.io/pkg/log"
	"net"
	"strconv"
	"time"
)

var mux *sm.StateMachine
var done = make(chan *diam.Message, 1000)
var cfg *sm.Settings
var conn diam.Conn

func GetUnits(ocsAddress string, applicationId string, requestUnits []string, used int) int {
	createMux(ocsAddress)
	forceUpdate := false
	for i := range requestUnits {
		units, err := strconv.Atoi(requestUnits[i])
		if err != nil {
			log.Fatalf("Couldn't parse a value %v", requestUnits[0])
		}
		if used != 0 || forceUpdate {
			sendCCRU(applicationId, used, units)
		} else {
			sendCCRI(applicationId, units)
		}
		select {
		case m := <-done:
			resultCodeAvp, err := m.FindAVP(avp.ResultCode, 0)
			if err != nil {
				log.Warnf("Couldn't find Result-Code AVP in response for %s", applicationId)
				return 0
			}
			resultCode := int(resultCodeAvp.Data.(datatype.Unsigned32))
			if resultCode == 4012 {
				used = 0
				forceUpdate = true
				continue
			}
			timeAvp, err := m.FindAVP(avp.CCTime, 0)
			if err != nil {
				log.Warnf("Couldn't find CC-Time AVP in response for %s", applicationId)
				return 0
			}
			grantedAmount := int(timeAvp.Data.(datatype.Unsigned32))
			log.Infof("grantedAmount: %v", grantedAmount)
			return grantedAmount
		case <-time.After(5 * time.Second):
			log.Fatalf("timeout: no hello answer received")
		}
	}
	return 0
}

func Terminate(usedMap map[string]int) {
	if conn == nil {
		return
	}
	for applicationId, used := range usedMap {
		sendCCRT(applicationId, used)
	}
}

func createMux(ocsAddress string) {
	if mux != nil && conn.RemoteAddr().String() == ocsAddress {
		return
	}
	host := "client"
	realm := "ocs-adapter"
	networkType := "tcp"

	cfg = &sm.Settings{
		OriginHost:       datatype.DiameterIdentity(host),
		OriginRealm:      datatype.DiameterIdentity(realm),
		VendorID:         13,
		ProductName:      "go-diameter",
		OriginStateID:    datatype.Unsigned32(time.Now().Unix()),
		FirmwareRevision: 1,
		HostIPAddresses: []datatype.Address{
			datatype.Address(net.ParseIP("127.0.0.1")),
		},
	}

	// Create the state machine (it's a diam.ServeMux) and client.
	mux = sm.New(cfg)

	cli := &sm.Client{
		Dict:               dict.Default,
		Handler:            mux,
		MaxRetransmits:     0,
		RetransmitInterval: time.Second,
		EnableWatchdog:     false,
		WatchdogInterval:   5 * time.Second,
		SupportedVendorID: []*diam.AVP{
			diam.NewAVP(avp.SupportedVendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		},
		VendorSpecificApplicationID: []*diam.AVP{
			diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(4)),
					diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
				},
			}),
		},
	}

	// Set message handlers.
	mux.Handle(diam.CCA, handleCCA())

	// Print error reports.
	go printErrors(mux.ErrorReports())

	// Makes a persistent connection with back-off.
	c, err := cli.DialNetwork(networkType, ocsAddress)
	if err != nil {
		log.Fatalf(err.Error())
	}
	conn = c
}

func printErrors(ec <-chan *diam.ErrorReport) {
	for err := range ec {
		log.Fatalf(err.String())
	}
}

func sendCCRI(applicationId string, requestUnits int) {
	log.Infof("send CCR to %v", conn.RemoteAddr().String())
	meta, ok := smpeer.FromContext(conn.Context())
	if !ok {
		log.Fatalf("Client connection does not contain metadata")
	}
	subscriptionId, _ := strconv.ParseUint(applicationId, 10, 32)
	m := diam.NewRequest(diam.CreditControl, 4, dict.Default)
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(4))
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(strconv.Itoa(0)))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, cfg.OriginHost)
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, cfg.OriginRealm)
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, meta.OriginRealm)
	m.NewAVP(avp.ServiceContextID, avp.Mbit, 0, datatype.OctetString("32251@3gpp.org"))
	m.NewAVP(avp.SubscriptionID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.SubscriptionIDType, avp.Mbit, 0, datatype.Unsigned32(3)),
			diam.NewAVP(avp.SubscriptionIDData, avp.Mbit, 0, datatype.OctetString(applicationId)),
		},
	})
	m.NewAVP(avp.EventTimestamp, avp.Mbit, 0, datatype.Unsigned32(time.Now().Unix()))
	m.NewAVP(avp.CCRequestType, avp.Mbit, 0, datatype.Unsigned32(1))
	m.NewAVP(avp.CCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(0))
	m.NewAVP(avp.MultipleServicesCreditControl, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.ServiceIdentifier, avp.Mbit, 0, datatype.Unsigned32(uint32(subscriptionId))),
			diam.NewAVP(avp.RatingGroup, avp.Mbit, 0, datatype.Unsigned32(0)),
			diam.NewAVP(avp.RequestedServiceUnit, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.CCTime, avp.Mbit, 0, datatype.Unsigned32(requestUnits)),
				},
			}),
		},
	})
	if _, err := m.WriteTo(conn); err != nil {
		log.Fatalf(err.Error())
	}
}

func sendCCRU(applicationId string, used int, requestUnits int) {
	log.Infof("send CCR to %v", conn.RemoteAddr().String())
	meta, ok := smpeer.FromContext(conn.Context())
	if !ok {
		log.Fatalf("Client connection does not contain metadata")
	}
	subscriptionId, _ := strconv.ParseUint(applicationId, 10, 32)
	m := diam.NewRequest(diam.CreditControl, 4, dict.Default)
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(4))
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(strconv.Itoa(0)))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, cfg.OriginHost)
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, cfg.OriginRealm)
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, meta.OriginRealm)
	m.NewAVP(avp.ServiceContextID, avp.Mbit, 0, datatype.OctetString("32251@3gpp.org"))
	m.NewAVP(avp.SubscriptionID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.SubscriptionIDType, avp.Mbit, 0, datatype.Unsigned32(3)),
			diam.NewAVP(avp.SubscriptionIDData, avp.Mbit, 0, datatype.OctetString(applicationId)),
		},
	})
	m.NewAVP(avp.EventTimestamp, avp.Mbit, 0, datatype.Unsigned32(time.Now().Unix()))
	m.NewAVP(avp.CCRequestType, avp.Mbit, 0, datatype.Unsigned32(2))
	m.NewAVP(avp.CCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(0))
	m.NewAVP(avp.MultipleServicesCreditControl, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.ServiceIdentifier, avp.Mbit, 0, datatype.Unsigned32(uint32(subscriptionId))),
			diam.NewAVP(avp.RatingGroup, avp.Mbit, 0, datatype.Unsigned32(0)),
			diam.NewAVP(avp.UsedServiceUnit, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.CCTime, avp.Mbit, 0, datatype.Unsigned32(used)),
				},
			}),
			diam.NewAVP(avp.RequestedServiceUnit, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.CCTime, avp.Mbit, 0, datatype.Unsigned32(requestUnits)),
				},
			}),
		},
	})
	if _, err := m.WriteTo(conn); err != nil {
		log.Fatalf(err.Error())
	}
}

func sendCCRT(applicationId string, used int) {
	log.Infof("send CCR to %v", conn.RemoteAddr().String())
	meta, ok := smpeer.FromContext(conn.Context())
	if !ok {
		log.Fatalf("Client connection does not contain metadata")
	}
	subscriptionId, _ := strconv.ParseUint(applicationId, 10, 32)
	m := diam.NewRequest(diam.CreditControl, 4, dict.Default)
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(4))
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(strconv.Itoa(0)))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, cfg.OriginHost)
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, cfg.OriginRealm)
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, meta.OriginRealm)
	m.NewAVP(avp.ServiceContextID, avp.Mbit, 0, datatype.OctetString("32251@3gpp.org"))
	m.NewAVP(avp.SubscriptionID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.SubscriptionIDType, avp.Mbit, 0, datatype.Unsigned32(3)),
			diam.NewAVP(avp.SubscriptionIDData, avp.Mbit, 0, datatype.OctetString(applicationId)),
		},
	})
	m.NewAVP(avp.EventTimestamp, avp.Mbit, 0, datatype.Unsigned32(time.Now().Unix()))
	m.NewAVP(avp.CCRequestType, avp.Mbit, 0, datatype.Unsigned32(3))
	m.NewAVP(avp.CCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(0))
	m.NewAVP(avp.MultipleServicesCreditControl, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.ServiceIdentifier, avp.Mbit, 0, datatype.Unsigned32(uint32(subscriptionId))),
			diam.NewAVP(avp.RatingGroup, avp.Mbit, 0, datatype.Unsigned32(0)),
			diam.NewAVP(avp.UsedServiceUnit, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.CCTime, avp.Mbit, 0, datatype.Unsigned32(used)),
				},
			}),
		},
	})
	if _, err := m.WriteTo(conn); err != nil {
		log.Fatalf(err.Error())
	}
}

func handleCCA() diam.HandlerFunc {
	return func(c diam.Conn, m *diam.Message) {
		log.Infof("Received CCA from %s\n%s", c.RemoteAddr(), m)
		done <- m
	}
}
