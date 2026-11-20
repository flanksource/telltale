package read

import "github.com/flanksource/commons-db/recordstore/sampleframe"

func latestFrameIndex(frames []sampleframe.Frame, source string) int {
	latest := -1
	for i := len(frames) - 1; i >= 0; i-- {
		if complete, exists := frames[i].Summary["complete"].(bool); exists && !complete {
			continue
		}
		if source != "" && frames[i].Source != source || latest >= 0 && frames[i].Source != frames[latest].Source {
			continue
		}
		if latest < 0 {
			latest = i
			source = frames[i].Source
		} else if frames[i].Tick > frames[latest].Tick {
			latest = i
		}
	}
	return latest
}
