package disablement

import (
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

var stateFromWire = map[pb.DisablementState]app.DisablementState{pb.DisablementState_DISABLEMENT_STATE_UNSPECIFIED: "", pb.DisablementState_DISABLEMENT_STATE_SCHEDULED: app.Scheduled, pb.DisablementState_DISABLEMENT_STATE_ACTIVE: app.Active, pb.DisablementState_DISABLEMENT_STATE_ENDED: app.Ended, pb.DisablementState_DISABLEMENT_STATE_CANCELLED: app.Cancelled}
