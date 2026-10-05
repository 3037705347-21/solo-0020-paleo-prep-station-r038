package station

import (
	"strconv"

	"example.com/paleo-prep-station/internal/contracts"
)

func notFoundSpecimen(id string) error {
	return contracts.NotFound("specimen_not_found", "未找到标本 "+id)
}

func notFoundPlan(id string) error {
	return contracts.NotFound("plan_not_found", "未找到修复方案 "+id)
}

func notFoundBatch(id string) error {
	return contracts.NotFound("batch_not_found", "未找到制备批次 "+id)
}

func notFoundStage(batchID string, stageID int) error {
	return contracts.NotFound("stage_fact_not_found", "批次 "+batchID+" 不存在阶段事实 "+strconv.Itoa(stageID))
}
