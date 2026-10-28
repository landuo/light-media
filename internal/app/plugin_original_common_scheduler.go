package app

import (
	"time"

	"light-media/internal/plugins/schedule"
)

func originalServiceLookupKey(job *originalDirectJob) originalServiceKey {
	if job.commonModule {
		return originalServiceKey{"", "scheduler_job"}
	}
	return originalServiceKey{job.object.installation.InstanceID, job.definition.ID}
}

func originalServiceActive(job *originalDirectJob) bool {
	return job.commonModule || originalObjectStillLoaded(job.object)
}

// Fixed Scheduler.init registers one common 10-minute service. It asks the
// current enabled plugin objects on each run; it is not bound to any plugin's
// get_service declarations, reload lifetime or persisted job receipts.
func (a *App) initializeOriginalCommonService(at time.Time) {
	spec := schedule.Spec{Kind: "interval", IntervalMS: (10 * time.Minute).Milliseconds(), Timezone: "UTC"}
	trigger, _ := schedule.New(spec, at)
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.jobs == nil {
		state.jobs = map[originalServiceKey]*originalDirectJob{}
	}
	state.nextRegistration++
	state.jobs[originalServiceKey{"", "scheduler_job"}] = &originalDirectJob{
		commonModule: true,
		definition:   pluginScheduleDefinition{ID: "scheduler_job", Name: "公共定时服务", Trigger: spec, MisfireGraceMS: 1000},
		trigger:      trigger, due: trigger.First(at), clockRegistered: true,
		timerID: "scheduler_job", registrationOrder: state.nextRegistration,
	}
}
