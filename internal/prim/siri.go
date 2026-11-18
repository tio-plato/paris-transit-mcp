package prim

import "time"

// StopMonitoring is the part of a SIRI Lite response we read.
type StopMonitoring struct {
	Siri struct {
		ServiceDelivery struct {
			StopMonitoringDelivery []struct {
				MonitoredStopVisit []Visit `json:"MonitoredStopVisit"`
			} `json:"StopMonitoringDelivery"`
		} `json:"ServiceDelivery"`
	} `json:"Siri"`
}

func (sm *StopMonitoring) Visits() []Visit {
	var out []Visit
	for _, d := range sm.Siri.ServiceDelivery.StopMonitoringDelivery {
		out = append(out, d.MonitoredStopVisit...)
	}
	return out
}

type Value struct {
	Value string `json:"value"`
}

type Visit struct {
	MonitoredVehicleJourney struct {
		LineRef            Value   `json:"LineRef"`
		DirectionRef       Value   `json:"DirectionRef"`
		DirectionName      []Value `json:"DirectionName"`
		DestinationName    []Value `json:"DestinationName"`
		VehicleJourneyName []Value `json:"VehicleJourneyName"`
		TrainNumbers       struct {
			TrainNumberRef []Value `json:"TrainNumberRef"`
		} `json:"TrainNumbers"`
		MonitoredCall struct {
			DestinationDisplay    []Value    `json:"DestinationDisplay"`
			AimedDepartureTime    *time.Time `json:"AimedDepartureTime"`
			ExpectedDepartureTime *time.Time `json:"ExpectedDepartureTime"`
			DepartureStatus       string     `json:"DepartureStatus"`
			DeparturePlatformName Value      `json:"DeparturePlatformName"`
		} `json:"MonitoredCall"`
	} `json:"MonitoredVehicleJourney"`
}

// Departure is the expected time, else the aimed one. ok is false for a
// train that terminates here.
func (v *Visit) Departure() (time.Time, bool) {
	call := v.MonitoredVehicleJourney.MonitoredCall
	switch {
	case call.ExpectedDepartureTime != nil:
		return *call.ExpectedDepartureTime, true
	case call.AimedDepartureTime != nil:
		return *call.AimedDepartureTime, true
	default:
		return time.Time{}, false
	}
}

func (v *Visit) Destination() string {
	j := v.MonitoredVehicleJourney
	if len(j.DestinationName) > 0 && j.DestinationName[0].Value != "" {
		return j.DestinationName[0].Value
	}
	if len(j.MonitoredCall.DestinationDisplay) > 0 {
		return j.MonitoredCall.DestinationDisplay[0].Value
	}
	return ""
}

func (v *Visit) HasTrainNumber(n string) bool {
	j := v.MonitoredVehicleJourney
	for _, list := range [][]Value{j.VehicleJourneyName, j.TrainNumbers.TrainNumberRef} {
		for _, x := range list {
			if x.Value == n {
				return true
			}
		}
	}
	return false
}
