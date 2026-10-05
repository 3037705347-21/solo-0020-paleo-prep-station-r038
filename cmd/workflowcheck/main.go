package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
	BatchID    string `json:"batch_id"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type planDetailView struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int    `json:"percent"`
		StageID string `json:"stage_id"`
		BatchID string `json:"batch_id"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type batchView struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	SiteLabel string `json:"site_label"`
	Members   []struct {
		PlanID string `json:"plan_id"`
	} `json:"members"`
}

type batchSnapshotView struct {
	Batch struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		SiteLabel string `json:"site_label"`
	} `json:"batch"`
	Members []struct {
		PlanID        string `json:"plan_id"`
		SpecimenID    string `json:"specimen_id"`
		PlanStatus    string `json:"plan_status"`
		LatestPercent int    `json:"latest_percent"`
		ProgressCount int    `json:"progress_count"`
	} `json:"members"`
	Stages []struct {
		ID       string `json:"id"`
		Sequence int    `json:"sequence"`
		Stage    string `json:"stage"`
	} `json:"stages"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL, "PPS-2026-001")
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL, "PPS-2026-001")
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "batch-shared-progress":
		return checkBatchSharedProgress(client, server.URL)

	case "batch-atomic-failure":
		return checkBatchAtomicFailure(client, server.URL)

	case "batch-split":
		return checkBatchSplit(client, server.URL)

	case "batch-cancel":
		return checkBatchCancel(client, server.URL)

	case "batch-member-cancel":
		return checkBatchMemberCancel(client, server.URL)

	case "batch-concurrent":
		return checkBatchConcurrent(client, server.URL)

	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// checkBatchSharedProgress 验证共享阶段事实与独立比例/终态：
// 同一阶段为整批生成一个 stage，各标本保留自己的比例序列；单份完成不提前关批。
func checkBatchSharedProgress(client *http.Client, baseURL string) error {
	specimens, plans, err := setupBatchPlans(client, baseURL, "PPS-SH-", 3)
	if err != nil {
		return err
	}
	batch, err := createBatch(client, baseURL, planIDs(plans), "北坡-07", "lead-he")
	if err != nil {
		return err
	}
	if batch.Status != "active" || len(batch.Members) != 3 {
		return fmt.Errorf("new batch should be active with 3 members, got %s/%d", batch.Status, len(batch.Members))
	}

	// 批次进行期间，成员方案不能绕过共享阶段单独写入进度。
	if err := expectStatus(client, http.MethodPost, baseURL+"/plans/"+plans[0].ID+"/progress",
		contracts.ProgressRequest{
			Stage:      "绕过批次",
			Percent:    10,
			RecordedBy: "preparator-chen",
		}, http.StatusConflict); err != nil {
		return err
	}

	snapshot, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "围岩粗剥",
		Note:       "整批同阶段",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 40},
			{PlanID: plans[1].ID, Percent: 30},
			{PlanID: plans[2].ID, Percent: 50},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("shared stage should be 201, got %d: %s", status, string(snapshot))
	}
	var created batchSnapshotView
	if err := json.Unmarshal(snapshot, &created); err != nil {
		return err
	}
	if created.Batch.Status != "active" || len(created.Stages) != 1 {
		return errors.New("batch should stay active with one shared stage")
	}
	if err := assertMember(created, plans[0].ID, "active", 40, 1); err != nil {
		return err
	}
	if err := assertMember(created, plans[1].ID, "active", 30, 1); err != nil {
		return err
	}
	if err := assertMember(created, plans[2].ID, "active", 50, 1); err != nil {
		return err
	}

	// 三条独立进度必须回链同一个共享阶段事实。
	var sharedStageID string
	for index, plan := range plans {
		var detail planDetailView
		if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plan.ID, nil, http.StatusOK, &detail); err != nil {
			return err
		}
		if len(detail.Progress) != 1 || detail.Progress[0].StageID == "" || detail.Plan.BatchID != batch.ID {
			return fmt.Errorf("plan %d progress should link to a shared stage of its batch", index)
		}
		if index == 0 {
			sharedStageID = detail.Progress[0].StageID
		} else if detail.Progress[0].StageID != sharedStageID {
			return errors.New("every specimen entry of one stage must share the same stage id")
		}
	}

	// 第二阶段：第一份标本达到 100，只关闭它自己的方案与标本，批次保持 active。
	body, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "关节面精修",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 100},
			{PlanID: plans[1].ID, Percent: 60},
			{PlanID: plans[2].ID, Percent: 80},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("second stage should be 201, got %d: %s", status, string(body))
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return err
	}
	if created.Batch.Status != "active" {
		return errors.New("batch must not close when only one specimen is finished")
	}
	if err := assertMember(created, plans[0].ID, "completed", 100, 2); err != nil {
		return err
	}
	var specimenZero specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimens[0].ID, nil, http.StatusOK, &specimenZero); err != nil {
		return err
	}
	if specimenZero.Status != "stabilized" {
		return fmt.Errorf("finished specimen should stabilize independently, got %s", specimenZero.Status)
	}
	var specimenOne specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimens[1].ID, nil, http.StatusOK, &specimenOne); err != nil {
		return err
	}
	if specimenOne.Status != "preparing" {
		return fmt.Errorf("unfinished specimen must remain preparing, got %s", specimenOne.Status)
	}

	// 最后阶段只覆盖仍开放的成员；已完成成员不参与，整批在同一次提交中关闭。
	body, status, err = postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "表面封护",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[2].ID, Percent: 100},
			{PlanID: plans[1].ID, Percent: 100},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("final stage should be 201, got %d: %s", status, string(body))
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return err
	}
	if created.Batch.Status != "completed" || len(created.Stages) != 3 {
		return fmt.Errorf("batch should complete after all members closed, got %s/%d stages", created.Batch.Status, len(created.Stages))
	}
	for index, specimen := range specimens {
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "stabilized" {
			return fmt.Errorf("specimen %d should stabilize when its own plan completes, got %s", index, fetched.Status)
		}
	}
	var detailOne planDetailView
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plans[1].ID, nil, http.StatusOK, &detailOne); err != nil {
		return err
	}
	if len(detailOne.Progress) != 3 || detailOne.Progress[0].Percent != 30 || detailOne.Progress[2].Percent != 100 {
		return errors.New("each specimen must keep its own independent percent trajectory")
	}
	return nil
}

// checkBatchAtomicFailure 验证批次写入的整体成功/整体失败：
// 任一成员进度非法（回退或缺席）时，共享阶段和所有成员进度都不落库。
func checkBatchAtomicFailure(client *http.Client, baseURL string) error {
	_, plans, err := setupBatchPlans(client, baseURL, "PPS-AF-", 2)
	if err != nil {
		return err
	}
	batch, err := createBatch(client, baseURL, planIDs(plans), "北坡-08", "lead-he")
	if err != nil {
		return err
	}
	if _, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "首阶段",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 40},
			{PlanID: plans[1].ID, Percent: 25},
		},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("first batch stage failed (status %d): %w", status, err)
	}

	// 第二份成员比例回退：整批拒绝。
	_, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "非法回退",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 60},
			{PlanID: plans[1].ID, Percent: 20},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusConflict {
		return fmt.Errorf("regressing batch item must conflict the whole write, got %d", status)
	}

	// 缺席一个开放成员：整批拒绝。
	_, status, err = postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "缺席成员",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 60},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusConflict {
		return fmt.Errorf("batch write missing an open member must conflict, got %d", status)
	}

	snapshot, err := getBatch(client, baseURL, batch.ID)
	if err != nil {
		return err
	}
	if snapshot.Batch.Status != "active" || len(snapshot.Stages) != 1 {
		return errors.New("failed batch writes must not create stage facts")
	}
	if err := assertMember(snapshot, plans[0].ID, "active", 40, 1); err != nil {
		return err
	}
	if err := assertMember(snapshot, plans[1].ID, "active", 25, 1); err != nil {
		return err
	}

	// 批次关闭后不能再追加共享阶段。
	if _, status, err = postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "收尾",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 100},
			{PlanID: plans[1].ID, Percent: 100},
		},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("closing stage failed (status %d): %w", status, err)
	}
	_, status, err = postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "关闭之后",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 100},
			{PlanID: plans[1].ID, Percent: 100},
		},
	})
	if err != nil {
		return err
	}
	if status != http.StatusConflict {
		return fmt.Errorf("progress on a completed batch must conflict, got %d", status)
	}
	return nil
}

// checkBatchSplit 验证拆分的确定性结果：成员整体迁入新批、源批保留至少一个成员、
// 拆出后两批按各自开放成员独立记录进度。
func checkBatchSplit(client *http.Client, baseURL string) error {
	_, plans, err := setupBatchPlans(client, baseURL, "PPS-SP-", 3)
	if err != nil {
		return err
	}
	source, err := createBatch(client, baseURL, planIDs(plans), "北坡-09", "lead-he")
	if err != nil {
		return err
	}
	if _, status, err := postBatchProgressRaw(client, baseURL, source.ID, contracts.BatchProgressRequest{
		Stage:      "粗剥",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 33},
			{PlanID: plans[1].ID, Percent: 33},
			{PlanID: plans[2].ID, Percent: 33},
		},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("split setup stage failed (status %d): %w", status, err)
	}

	var target batchView
	if err := requestJSON(client, http.MethodPost, baseURL+"/batches/"+source.ID+"/split",
		contracts.SplitBatchRequest{PlanIDs: []string{plans[2].ID}, SiteLabel: "南坡-02"},
		http.StatusCreated, &target); err != nil {
		return err
	}
	if target.Status != "active" || target.SiteLabel != "南坡-02" || len(target.Members) != 1 || target.Members[0].PlanID != plans[2].ID {
		return errors.New("split target must contain exactly the moved member")
	}

	sourceView, err := getBatch(client, baseURL, source.ID)
	if err != nil {
		return err
	}
	if len(sourceView.Members) != 2 {
		return fmt.Errorf("source batch must keep two members, got %d", len(sourceView.Members))
	}
	for _, member := range sourceView.Members {
		if member.PlanID == plans[2].ID {
			return errors.New("moved plan must be removed from source batch membership")
		}
	}
	var moved planDetailView
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plans[2].ID, nil, http.StatusOK, &moved); err != nil {
		return err
	}
	if moved.Plan.BatchID != target.ID || len(moved.Progress) != 1 {
		return errors.New("moved plan must point at the new batch and keep its independent progress")
	}

	// 两批分别按各自开放成员记录进度。
	if _, status, err := postBatchProgressRaw(client, baseURL, target.ID, contracts.BatchProgressRequest{
		Stage:      "南坡精修",
		RecordedBy: "preparator-chen",
		Items:      []contracts.BatchProgressItem{{PlanID: plans[2].ID, Percent: 70}},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("moved batch progress failed (status %d): %w", status, err)
	}
	if _, status, err := postBatchProgressRaw(client, baseURL, source.ID, contracts.BatchProgressRequest{
		Stage:      "北坡精修",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 60},
			{PlanID: plans[1].ID, Percent: 60},
		},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("source batch progress failed (status %d): %w", status, err)
	}

	// 非法拆分：搬空源批、搬非成员。
	if err := expectStatus(client, http.MethodPost, baseURL+"/batches/"+source.ID+"/split",
		contracts.SplitBatchRequest{PlanIDs: []string{plans[0].ID, plans[1].ID}}, http.StatusConflict); err != nil {
		return err
	}
	if err := expectStatus(client, http.MethodPost, baseURL+"/batches/"+target.ID+"/split",
		contracts.SplitBatchRequest{PlanIDs: []string{plans[0].ID}}, http.StatusConflict); err != nil {
		return err
	}
	return nil
}

// checkBatchCancel 验证取消批次的结果：批次外壳关闭且成员记录保留，开放方案脱离批次
// 后继续独立流转，已完成方案保留历史归属指针。
func checkBatchCancel(client *http.Client, baseURL string) error {
	specimens, plans, err := setupBatchPlans(client, baseURL, "PPS-CX-", 3)
	if err != nil {
		return err
	}
	batch, err := createBatch(client, baseURL, planIDs(plans), "北坡-10", "lead-he")
	if err != nil {
		return err
	}
	if _, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "粗剥",
		RecordedBy: "preparator-chen",
		Items: []contracts.BatchProgressItem{
			{PlanID: plans[0].ID, Percent: 100},
			{PlanID: plans[1].ID, Percent: 40},
			{PlanID: plans[2].ID, Percent: 60},
		},
	}); err != nil || status != http.StatusCreated {
		return fmt.Errorf("cancel setup stage failed (status %d): %w", status, err)
	}

	var cancelled batchView
	if err := requestJSON(client, http.MethodPost, baseURL+"/batches/"+batch.ID+"/cancellation",
		contracts.CancelBatchRequest{CancelledBy: "lead-he"}, http.StatusOK, &cancelled); err != nil {
		return err
	}
	if cancelled.Status != "cancelled" {
		return fmt.Errorf("batch should be cancelled, got %s", cancelled.Status)
	}
	snapshot, err := getBatch(client, baseURL, batch.ID)
	if err != nil {
		return err
	}
	if snapshot.Batch.Status != "cancelled" || len(snapshot.Members) != 3 || len(snapshot.Stages) != 1 {
		return errors.New("cancelled batch must retain membership history and shared stages")
	}

	var openPlan planDetailView
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plans[1].ID, nil, http.StatusOK, &openPlan); err != nil {
		return err
	}
	if openPlan.Plan.BatchID != "" || openPlan.Plan.Status != "active" {
		return errors.New("open plans detach from a cancelled batch and keep their own status")
	}
	var donePlan planDetailView
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plans[0].ID, nil, http.StatusOK, &donePlan); err != nil {
		return err
	}
	if donePlan.Plan.BatchID != batch.ID || donePlan.Plan.Status != "completed" {
		return errors.New("completed plans keep the historical batch link")
	}

	// 脱离后的方案可以独立完成并稳定标本。
	if err := recordProgress(client, baseURL, plans[1].ID, 100, "独立收尾"); err != nil {
		return err
	}
	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimens[1].ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "stabilized" {
		return fmt.Errorf("detached specimen can still stabilize independently, got %s", fetched.Status)
	}
	// 取消后的批次拒绝再写阶段。
	if err := expectStatus(client, http.MethodPost, baseURL+"/batches/"+batch.ID+"/progress",
		contracts.BatchProgressRequest{
			Stage:      "取消之后",
			RecordedBy: "preparator-chen",
			Items:      []contracts.BatchProgressItem{{PlanID: plans[2].ID, Percent: 80}},
		}, http.StatusConflict); err != nil {
		return err
	}
	return nil
}

// checkBatchMemberCancel 验证单份草稿成员取消时的批次一致性：
// 未就绪成员会阻止整批阶段写入；取消后批次只在剩余方案全部终态时关闭。
func checkBatchMemberCancel(client *http.Client, baseURL string) error {
	planASpecimen, err := registerSpecimen(client, baseURL, "PPS-MC-001")
	if err != nil {
		return err
	}
	planA, err := createPlan(client, baseURL, planASpecimen.ID)
	if err != nil {
		return err
	}
	if err := approvePlan(client, baseURL, planA.ID); err != nil {
		return err
	}
	planDSpecimen, err := registerSpecimen(client, baseURL, "PPS-MC-002")
	if err != nil {
		return err
	}
	planD, err := createPlan(client, baseURL, planDSpecimen.ID)
	if err != nil {
		return err
	}
	// planD 保持草稿，模拟部分标本尚未就绪。
	batch, err := createBatch(client, baseURL, []string{planA.ID, planD.ID}, "北坡-11", "lead-he")
	if err != nil {
		return err
	}

	// 草稿成员未就绪：整批写入必须失败。
	if err := expectStatus(client, http.MethodPost, baseURL+"/batches/"+batch.ID+"/progress",
		contracts.BatchProgressRequest{
			Stage:      "提前开工",
			RecordedBy: "preparator-chen",
			Items: []contracts.BatchProgressItem{
				{PlanID: planA.ID, Percent: 50},
				{PlanID: planD.ID, Percent: 50},
			},
		}, http.StatusConflict); err != nil {
		return err
	}

	var cancelled planView
	if err := requestJSON(client, http.MethodPost, baseURL+"/plans/"+planD.ID+"/cancellation",
		contracts.CancelPlanRequest{CancelledBy: "lead-he"}, http.StatusOK, &cancelled); err != nil {
		return err
	}
	if cancelled.Status != "cancelled" || cancelled.BatchID == "" {
		return errors.New("cancelled member plan records who cancelled and keeps batch membership pointer")
	}
	snapshot, err := getBatch(client, baseURL, batch.ID)
	if err != nil {
		return err
	}
	if snapshot.Batch.Status != "active" {
		return errors.New("batch stays active while other members remain open")
	}
	if err := assertMember(snapshot, planD.ID, "cancelled", 0, 0); err != nil {
		return err
	}

	// 只剩一个开放成员时，批次进度只覆盖它；它完成即整体关批。
	body, status, err := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
		Stage:      "独立终段",
		RecordedBy: "preparator-chen",
		Items:      []contracts.BatchProgressItem{{PlanID: planA.ID, Percent: 100}},
	})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("final open member should close the batch, got %d: %s", status, string(body))
	}
	var final batchSnapshotView
	if err := json.Unmarshal(body, &final); err != nil {
		return err
	}
	if final.Batch.Status != "completed" {
		return fmt.Errorf("batch should complete once every member is closed, got %s", final.Batch.Status)
	}
	return nil
}

// checkBatchConcurrent 验证并发批次写入具有确定结果：
// 同一终段并发提交恰好一次成功；同一组方案并发建批恰好一次成功。
func checkBatchConcurrent(client *http.Client, baseURL string) error {
	_, plans, err := setupBatchPlans(client, baseURL, "PPS-CC-", 2)
	if err != nil {
		return err
	}
	batch, err := createBatch(client, baseURL, planIDs(plans), "北坡-12", "lead-he")
	if err != nil {
		return err
	}

	const writers = 20
	firstStart := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make(chan int, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-firstStart
			_, status, postErr := postBatchProgressRaw(client, baseURL, batch.ID, contracts.BatchProgressRequest{
				Stage:      "并发终段",
				RecordedBy: "preparator-chen",
				Items: []contracts.BatchProgressItem{
					{PlanID: plans[0].ID, Percent: 100},
					{PlanID: plans[1].ID, Percent: 100},
				},
			})
			if postErr != nil {
				status = 0
			}
			statuses <- status
		}()
	}
	close(firstStart)
	wg.Wait()
	close(statuses)

	created, conflicted := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
		default:
			return fmt.Errorf("unexpected concurrent status %d", status)
		}
	}
	if created != 1 || conflicted != writers-1 {
		return fmt.Errorf("expected exactly one winning stage write, got %d created and %d conflicts", created, conflicted)
	}
	for _, plan := range plans {
		var detail planDetailView
		if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plan.ID, nil, http.StatusOK, &detail); err != nil {
			return err
		}
		if len(detail.Progress) != 1 || detail.Plan.Status != "completed" {
			return errors.New("concurrent writes must not duplicate or lose progress entries")
		}
	}

	// 同一组方案并发建批：恰好一次成功，其余全部冲突。
	_, racePlans, err := setupBatchPlans(client, baseURL, "PPS-CC-R-", 2)
	if err != nil {
		return err
	}
	raceIDs := planIDs(racePlans)
	raceStatuses := make(chan int, writers)
	secondStart := make(chan struct{})
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-secondStart
			_, status, _ := postCreateBatchRaw(client, baseURL, contracts.CreateBatchRequest{
				SiteLabel:  "北坡-13",
				Supervisor: "lead-he",
				PlanIDs:    raceIDs,
			})
			raceStatuses <- status
		}()
	}
	close(secondStart)
	wg.Wait()
	close(raceStatuses)
	created, conflicted = 0, 0
	for status := range raceStatuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
		default:
			return fmt.Errorf("unexpected concurrent create status %d", status)
		}
	}
	if created != 1 || conflicted != writers-1 {
		return fmt.Errorf("expected exactly one winning batch creation, got %d created and %d conflicts", created, conflicted)
	}
	return nil
}

func registerSpecimen(client *http.Client, baseURL, catalog string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalog,
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func approvePlan(client *http.Client, baseURL, planID string) error {
	var approved planView
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	)
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL, "PPS-2026-001")
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	if err := approvePlan(client, baseURL, plan.ID); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, plan, nil
}

// setupBatchPlans 登记多份标本，为每份标本建立并审批一个独立方案。
func setupBatchPlans(client *http.Client, baseURL, catalogPrefix string, count int) ([]specimenView, []planView, error) {
	specimens := make([]specimenView, 0, count)
	plans := make([]planView, 0, count)
	for index := range count {
		specimen, err := registerSpecimen(client, baseURL, fmt.Sprintf("%s%03d", catalogPrefix, index+1))
		if err != nil {
			return nil, nil, err
		}
		plan, err := createPlan(client, baseURL, specimen.ID)
		if err != nil {
			return nil, nil, err
		}
		if err := approvePlan(client, baseURL, plan.ID); err != nil {
			return nil, nil, err
		}
		specimens = append(specimens, specimen)
		plans = append(plans, plan)
	}
	return specimens, plans, nil
}

func planIDs(plans []planView) []string {
	ids := make([]string, 0, len(plans))
	for _, plan := range plans {
		ids = append(ids, plan.ID)
	}
	return ids
}

func createBatch(client *http.Client, baseURL string, ids []string, site, supervisor string) (batchView, error) {
	var batch batchView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/batches",
		contracts.CreateBatchRequest{SiteLabel: site, Supervisor: supervisor, PlanIDs: ids},
		http.StatusCreated,
		&batch,
	)
	return batch, err
}

func getBatch(client *http.Client, baseURL, batchID string) (batchSnapshotView, error) {
	var snapshot batchSnapshotView
	err := requestJSON(client, http.MethodGet, baseURL+"/batches/"+batchID, nil, http.StatusOK, &snapshot)
	return snapshot, err
}

func assertMember(snapshot batchSnapshotView, planID, wantStatus string, wantPercent, wantCount int) error {
	for _, member := range snapshot.Members {
		if member.PlanID == planID {
			if member.PlanStatus != wantStatus || member.LatestPercent != wantPercent || member.ProgressCount != wantCount {
				return fmt.Errorf(
					"member %s mismatch: got status=%s percent=%d count=%d, want %s/%d/%d",
					planID, member.PlanStatus, member.LatestPercent, member.ProgressCount,
					wantStatus, wantPercent, wantCount,
				)
			}
			return nil
		}
	}
	return fmt.Errorf("member %s missing from batch snapshot", planID)
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func postBatchProgressRaw(client *http.Client, baseURL, batchID string, request contracts.BatchProgressRequest) ([]byte, int, error) {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/batches/"+batchID+"/progress", request)
	return raw, status, err
}

func postCreateBatchRaw(client *http.Client, baseURL string, request contracts.CreateBatchRequest) ([]byte, int, error) {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/batches", request)
	return raw, status, err
}

func expectStatus(client *http.Client, method, url string, body any, wantStatus int) error {
	status, raw, err := doJSON(client, method, url, body)
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("request %s returned %d, want %d: %s", url, status, wantStatus, string(raw))
	}
	return nil
}

func doJSON(client *http.Client, method, url string, body any) (int, []byte, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, raw, nil
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	status, raw, err := doJSON(client, method, url, body)
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, status, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
