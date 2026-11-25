package brushflowlanduo

import (
	"context"
	"fmt"
	"strconv"
)

// SizeReadHost is an owner-side read projection, never a worker-supplied size
// estimate. The final App host must bind it to actual configured downloaders.
type SizeReadHost interface {
	DownloadSizes(context.Context, string) (exists, active bool, sizes []float64, err error)
}

type SizeTask struct {
	Downloader string
	DiskSize   *float64
}

// TotalDownloaderSize follows the original global limit calculation: query
// every referenced downloader once and count all of its torrents, including
// torrents that carry no brush task tag. No candidate size is predicted here.
func TotalDownloaderSize(ctx context.Context, tasks []SizeTask, host SizeReadHost) (float64, error) {
	seen := map[string]bool{}
	var total float64
	for _, task := range tasks {
		if task.Downloader == "" || seen[task.Downloader] {
			continue
		}
		seen[task.Downloader] = true
		exists, active, sizes, err := host.DownloadSizes(ctx, task.Downloader)
		if !exists || !active {
			return 0, fmt.Errorf("无法获取下载器 [%s] 实时种子体积", task.Downloader)
		}
		if err != nil {
			return 0, fmt.Errorf("获取下载器 [%s] 种子失败", task.Downloader)
		}
		for _, size := range sizes {
			total += size
		}
	}
	return total, nil
}

func BytesToGB(size float64) float64 { return size / (1024 * 1024 * 1024) }

// CanAddSize predicts the result of adding one torrent. A candidate that would
// push the task or downloader past its limit is not sent at all; the task then
// waits until deleted seeds free the space.
func CanAddSize(ctx context.Context, task *SizeTask, current, adding float64, globalLimit *float64, globalCurrent float64, tasks []SizeTask, host SizeReadHost) (bool, string, error) {
	if task == nil {
		return false, "任务配置不存在", nil
	}
	if adding < 0 {
		adding = 0
	}
	if task.DiskSize != nil && *task.DiskSize != 0 && current+adding >= *task.DiskSize*(1024*1024*1024) {
		return false, fmt.Sprintf("添加后将达到 %.1f GB，超过任务保种上限 %s GB，等待保种释放", BytesToGB(current+adding), strconv.FormatFloat(*task.DiskSize, 'f', -1, 64)), nil
	}
	if globalLimit != nil && *globalLimit != 0 {
		total := globalCurrent
		if total == 0 {
			var err error
			total, err = TotalDownloaderSize(ctx, tasks, host)
			if err != nil {
				return false, "", err
			}
		}
		if total+adding >= *globalLimit*(1024*1024*1024) {
			return false, fmt.Sprintf("添加后将达到 %.1f GB，超过全局保种上限 %s GB，等待保种释放", BytesToGB(total+adding), strconv.FormatFloat(*globalLimit, 'f', -1, 64)), nil
		}
	}
	return true, "", nil
}

// EvaluateSizeCondition preserves the original >= boundary and task-before-
// global precedence. A supplied global snapshot avoids another daemon read.
func EvaluateSizeCondition(ctx context.Context, task *SizeTask, taskSize float64, globalLimit, globalSize *float64, tasks []SizeTask, host SizeReadHost) (bool, string, error) {
	if task == nil {
		return false, "任务配置不存在", nil
	}
	if task.DiskSize != nil && *task.DiskSize != 0 && taskSize >= *task.DiskSize*(1024*1024*1024) {
		return false, fmt.Sprintf("当前做种体积 %.1f GB，已达到任务保种上限 %s GB", BytesToGB(taskSize), strconv.FormatFloat(*task.DiskSize, 'f', -1, 64)), nil
	}
	if globalLimit != nil && *globalLimit != 0 {
		var total float64
		if globalSize != nil {
			total = *globalSize
		} else {
			var err error
			total, err = TotalDownloaderSize(ctx, tasks, host)
			if err != nil {
				return false, "", err
			}
		}
		if total >= *globalLimit*(1024*1024*1024) {
			return false, fmt.Sprintf("下载器当前种子总体积 %.1f GB，已达到全局保种上限 %s GB", BytesToGB(total), strconv.FormatFloat(*globalLimit, 'f', -1, 64)), nil
		}
	}
	return true, "", nil
}
