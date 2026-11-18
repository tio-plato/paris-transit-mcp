package prim

import "time"

// StopMonitoring is the part of a SIRI Lite stop-monitoring response the server reads.
type StopMonitoring struct {
	Siri struct {
		ServiceDelivery struct {
			StopMonitoringDelivery []struct {
				MonitoredStopVisit []Visit `json:"MonitoredStopVisit"`
			} `json:"StopMonitoringDelivery"`
		} `json:"ServiceDelivery"`
	} `json:"Siri"`
}

// Visits returns the visits of every delivery.
func (sm *StopMonitoring) Visits() []Visit {
	var out []Visit
	for _, d := range sm.Siri.ServiceDelivery.StopMonitoringDelivery {
		out = append(out, d.MonitoredStopVisit...)
	}
	return out
}

// Value is SIRI's wrapper around a string.
type Value struct {
	Value string `json:"value"`
}

// Visit is one vehicle calling at the monitored stop.
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

// Departure is the expected departure time, or the aimed one when SIRI gives
// no expected time; ok is false when the visit has no departure, such as a
// train that ends its run at this stop.
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

// Destination is the destination name, or the destination display when SIRI gives no name.
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

// HasTrainNumber reports whether the visit's vehicle journey name or train
// number is n.
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
