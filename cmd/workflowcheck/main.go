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
	BatchID    string `json:"batch_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent      int `json:"percent"`
		BatchStageID int `json:"batch_stage_id"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type batchSnapshot struct {
	Batch struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Site   string `json:"site"`
	} `json:"batch"`
	Members []struct {
		Plan struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"plan"`
		SpecimenID    string `json:"specimen_id"`
		LatestPercent int    `json:"latest_percent"`
	} `json:"members"`
	Stages []struct {
		Fact struct {
			ID       int    `json:"id"`
			Sequence int    `json:"sequence"`
			Stage    string `json:"stage"`
		} `json:"fact"`
		Entries []struct {
			PlanID  string `json:"plan_id"`
			Percent int    `json:"percent"`
		} `json:"entries"`
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

	case "prepare-multi-specimen-batch":
		return checkBatchLifecycle(client, server.URL)

	case "batch-concurrent-progress":
		return checkBatchConcurrency(client, server.URL)

	case "batch-cancel-and-split":
		return checkBatchCancelAndSplit(client, server.URL)

	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// checkBatchLifecycle verifies shared stage facts, independent percentages and
// terminal states, and that one member finishing never closes the batch.
func checkBatchLifecycle(client *http.Client, baseURL string) error {
	batch, memberPlans, err := createApprovedBatch(client, baseURL, "PPS-BA-00")
	if err != nil {
		return err
	}

	// First shared stage: members record independent percentages in one write.
	stage1, err := recordBatchStage(client, baseURL, batch, contracts.RecordBatchStageRequest{
		Stage:      "围岩去除",
		Note:       "批次首次共享阶段",
		RecordedBy: "preparator-chen",
		Entries: []contracts.BatchStageEntrySpec{
			{PlanID: memberPlans[0], Percent: 30},
			{PlanID: memberPlans[1], Percent: 40},
			{PlanID: memberPlans[2], Percent: 50},
		},
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if stage1.Fact.Sequence != 1 || len(stage1.Entries) != 3 {
		return errors.New("first shared stage fact was not written for all members")
	}

	// One member completes via its own plan endpoint: batch must stay open.
	if err := postProgress(client, baseURL, memberPlans[0], contracts.ProgressRequest{
		Stage:      "表面加固",
		Percent:    100,
		Note:       "提前完成的成员",
		RecordedBy: "preparator-chen",
	}); err != nil {
		return err
	}
	if err := expectBatchStatus(client, baseURL, batch, "open"); err != nil {
		return err
	}

	// Closing now must fail because two members are still active.
	if err := closeBatchExpect(client, baseURL, batch, http.StatusConflict, "batch_has_open_members"); err != nil {
		return err
	}

	// Anchor member 2 to the existing shared stage fact via the plan endpoint.
	if err := postProgress(client, baseURL, memberPlans[1], contracts.ProgressRequest{
		BatchStageID: stage1.Fact.ID,
		Percent:      100,
		Note:         "引用共享阶段事实",
		RecordedBy:   "preparator-chen",
	}); err != nil {
		return err
	}
	if err := expectBatchStatus(client, baseURL, batch, "open"); err != nil {
		return err
	}

	// A shared stage fact can be written even when some members skip entries.
	if _, err := recordBatchStage(client, baseURL, batch, contracts.RecordBatchStageRequest{
		Stage:      "终检",
		Note:       "最后一个成员单独记录",
		RecordedBy: "preparator-chen",
		Entries: []contracts.BatchStageEntrySpec{
			{PlanID: memberPlans[2], Percent: 100},
		},
	}, http.StatusCreated); err != nil {
		return err
	}

	snapshot, err := closeBatch(client, baseURL, batch)
	if err != nil {
		return err
	}
	if snapshot.Batch.Status != "completed" {
		return fmt.Errorf("expected completed batch, got %s", snapshot.Batch.Status)
	}
	if len(snapshot.Members) != 3 {
		return fmt.Errorf("expected 3 members, got %d", len(snapshot.Members))
	}
	for _, member := range snapshot.Members {
		if member.Plan.Status != "completed" || member.LatestPercent != 100 {
			return fmt.Errorf("member %s did not keep an independent completed state: %s %d%%",
				member.Plan.ID, member.Plan.Status, member.LatestPercent)
		}
	}

	// Closed batch rejects further shared stage writes.
	if err := recordBatchStageExpect(client, baseURL, batch, contracts.RecordBatchStageRequest{
		Stage:      "复查",
		RecordedBy: "preparator-chen",
	}, http.StatusConflict, "batch_not_open"); err != nil {
		return err
	}

	// One of the specimens can still be released independently after closure.
	specimen, err := specimenForPlan(client, baseURL, memberPlans[0])
	if err != nil {
		return err
	}
	var release releaseView
	if err := requestJSON(client, http.MethodPost, baseURL+"/specimens/"+specimen+"/release",
		contracts.ReleaseRequest{Purpose: "批次后研究", Recipient: "古脊椎组", ApprovedBy: "keeper-zhou"},
		http.StatusCreated, &release); err != nil {
		return err
	}
	return nil
}

// checkBatchConcurrency fires concurrent shared-stage writes and concurrent
// per-plan progress writes, then asserts deterministic sequences, exactly one
// winner per monotonic race, and a close that succeeds only once all members
// finish.
func checkBatchConcurrency(client *http.Client, baseURL string) error {
	batch, memberPlans, err := createApprovedBatch(client, baseURL, "PPS-BC-00")
	if err != nil {
		return err
	}

	const stageWriters = 8
	var wg sync.WaitGroup
	stageIDs := make(chan int, stageWriters)
	errs := make(chan error, stageWriters+4)
	wg.Add(stageWriters)
	for i := 0; i < stageWriters; i++ {
		i := i
		go func() {
			defer wg.Done()
			stage, err := recordBatchStage(client, baseURL, batch,
				contracts.RecordBatchStageRequest{
					Stage:      fmt.Sprintf("并行阶段-%d", i),
					RecordedBy: "preparator-chen",
				}, http.StatusCreated)
			if err != nil {
				errs <- err
				return
			}
			stageIDs <- stage.Fact.ID
		}()
	}
	wg.Wait()
	close(stageIDs)
	seen := map[int]bool{}
	for id := range stageIDs {
		if seen[id] {
			return fmt.Errorf("duplicate stage fact id %d under concurrency", id)
		}
		seen[id] = true
	}

	// Concurrent distinct percentages on the same plan race under the mutex.
	// Every request commits once or conflicts, the trail never regresses and
	// the final state converges to the highest committed percentage.
	const racers = 12
	var raceWG sync.WaitGroup
	statuses := make(chan int, racers)
	raceWG.Add(racers)
	for i := 0; i < racers; i++ {
		percent := (i + 1) * 5 // 5..60
		go func() {
			defer raceWG.Done()
			statuses <- postProgressStatus(client, baseURL, memberPlans[0], contracts.ProgressRequest{
				Stage:      fmt.Sprintf("并行去基质-%d", percent),
				Percent:    percent,
				Note:       "不同比例的并发写入",
				RecordedBy: "preparator-chen",
			})
		}()
	}
	raceWG.Wait()
	close(statuses)
	okCount, conflictCount := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			okCount++
		case http.StatusConflict:
			conflictCount++
		default:
			return fmt.Errorf("unexpected status %d from concurrent progress", status)
		}
	}
	if okCount == 0 {
		return errors.New("at least one racer must commit progress")
	}
	if okCount+conflictCount != racers {
		return fmt.Errorf("racers produced unexpected outcomes: %d ok, %d conflict", okCount, conflictCount)
	}
	var racerPlan planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+memberPlans[0], nil, http.StatusOK, &racerPlan); err != nil {
		return err
	}
	if len(racerPlan.Progress) != okCount {
		return fmt.Errorf("expected %d committed entries, got %d", okCount, len(racerPlan.Progress))
	}
	for index, entry := range racerPlan.Progress {
		if index > 0 && entry.Percent < racerPlan.Progress[index-1].Percent {
			return fmt.Errorf("concurrent commits produced a regression: %+v", racerPlan.Progress)
		}
	}
	if racerPlan.Progress[len(racerPlan.Progress)-1].Percent != 60 {
		return fmt.Errorf("expected final percentage 60, got %d", racerPlan.Progress[len(racerPlan.Progress)-1].Percent)
	}
	select {
	case err := <-errs:
		return err
	default:
	}

	// Finish all members concurrently; completion must never touch batch state.
	finishStatuses := make(chan int, len(memberPlans))
	raceWG.Add(len(memberPlans))
	for _, planID := range memberPlans {
		planID := planID
		go func() {
			defer raceWG.Done()
			finishStatuses <- postProgressStatus(client, baseURL, planID, contracts.ProgressRequest{
				Stage:      "完工",
				Percent:    100,
				Note:       "并发完工",
				RecordedBy: "preparator-chen",
			})
		}()
	}
	raceWG.Wait()
	close(finishStatuses)
	for status := range finishStatuses {
		if status != http.StatusCreated {
			return fmt.Errorf("expected concurrent completions to succeed, got %d", status)
		}
	}

	snapshot, err := closeBatch(client, baseURL, batch)
	if err != nil {
		return err
	}
	if len(snapshot.Stages) != stageWriters {
		return fmt.Errorf("expected %d shared stage facts, got %d", stageWriters, len(snapshot.Stages))
	}
	for index, stage := range snapshot.Stages {
		if stage.Fact.Sequence != index+1 {
			return fmt.Errorf("stage facts not sequentially ordered: %+v", stage.Fact)
		}
	}
	for _, member := range snapshot.Members {
		if member.Plan.Status != "completed" {
			return fmt.Errorf("member %s unexpectedly %s", member.Plan.ID, member.Plan.Status)
		}
	}
	// Close is terminal: a second close deterministically conflicts.
	if err := closeBatchExpect(client, baseURL, batch, http.StatusConflict, "batch_not_open"); err != nil {
		return err
	}
	return nil
}

// checkBatchCancelAndSplit covers atomic batch creation failure, deterministic
// cancel boundaries, and draft-only split into a new batch.
func checkBatchCancelAndSplit(client *http.Client, baseURL string) error {
	// A duplicate catalog inside the batch request must roll everything back.
	bad, err := createBatchRaw(client, baseURL, contracts.CreateBatchRequest{
		Site:    "北坡七号坑",
		Purpose: "应当整体失败的批次",
		Owner:   "lead-guo",
		Members: []contracts.BatchMemberSpec{
			batchMemberSpec("PPS-BX-001"),
			batchMemberSpec("PPS-BX-001"),
		},
	}, http.StatusConflict, "duplicate_catalog_number")
	if err != nil {
		return err
	}
	if bad.Batch.ID != "" {
		return errors.New("failed batch creation leaked a batch id")
	}
	var specimens struct {
		Items []specimenView `json:"items"`
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens", nil, http.StatusOK, &specimens); err != nil {
		return err
	}
	for _, item := range specimens.Items {
		if item.CatalogNumber == "PPS-BX-001" {
			return errors.New("failed batch creation leaked specimen rows")
		}
	}

	// A fresh two-member draft batch cancels together with its draft plans.
	cancelBatchID, cancelPlans, err := createOpenBatch(client, baseURL, "PPS-BX-10")
	if err != nil {
		return err
	}
	cancelled, err := cancelBatch(client, baseURL, cancelBatchID)
	if err != nil {
		return err
	}
	if cancelled.Batch.Status != "cancelled" {
		return fmt.Errorf("expected cancelled batch, got %s", cancelled.Batch.Status)
	}
	for _, planID := range cancelPlans {
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+planID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "cancelled" {
			return fmt.Errorf("expected member plan cancelled, got %s", snapshot.Plan.Status)
		}
	}

	// After approval cancellation is rejected wholesale.
	approvedBatchID, approvedPlans, err := createApprovedBatch(client, baseURL, "PPS-BX-20")
	if err != nil {
		return err
	}
	if err := cancelBatchExpect(client, baseURL, approvedBatchID, http.StatusConflict, "batch_not_cancellable"); err != nil {
		return err
	}

	// Split only works on drafts: approved members must reject the operation.
	if err := splitBatchExpect(client, baseURL, approvedBatchID, []string{approvedPlans[1]},
		http.StatusConflict, "plan_not_splittable"); err != nil {
		return err
	}

	// A draft batch can split one member into its own batch deterministically.
	draftBatchID, draftPlans, err := createOpenBatch(client, baseURL, "PPS-BX-30")
	if err != nil {
		return err
	}
	split, err := splitBatch(client, baseURL, draftBatchID, []string{draftPlans[1]})
	if err != nil {
		return err
	}
	if split.Batch.Status != "open" || len(split.Members) != 1 {
		return fmt.Errorf("split batch unexpected shape: status=%s members=%d", split.Batch.Status, len(split.Members))
	}
	original, err := getBatch(client, baseURL, draftBatchID)
	if err != nil {
		return err
	}
	if len(original.Members) != 2 {
		return fmt.Errorf("source batch should keep two members, got %d", len(original.Members))
	}
	remaining := map[string]bool{draftPlans[0]: true, draftPlans[2]: true}
	for _, member := range original.Members {
		if !remaining[member.Plan.ID] {
			return fmt.Errorf("source batch kept unexpected plan %s", member.Plan.ID)
		}
	}
	// The split member now belongs only to the new batch.
	var moved planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+draftPlans[1], nil, http.StatusOK, &moved); err != nil {
		return err
	}
	if moved.Plan.BatchID != split.Batch.ID {
		return fmt.Errorf("split plan belongs to %s, expected %s", moved.Plan.BatchID, split.Batch.ID)
	}
	// Moving both remaining members out is rejected: source must survive.
	if err := splitBatchExpect(client, baseURL, draftBatchID, []string{draftPlans[0], draftPlans[2]},
		http.StatusConflict, "batch_split_empties_source"); err != nil {
		return err
	}
	// Moving one remaining member is allowed; then the last one is pinned.
	secondSplit, err := splitBatch(client, baseURL, draftBatchID, []string{draftPlans[2]})
	if err != nil {
		return err
	}
	if len(secondSplit.Members) != 1 {
		return fmt.Errorf("second split batch should have one member, got %d", len(secondSplit.Members))
	}
	if err := splitBatchExpect(client, baseURL, draftBatchID, []string{draftPlans[0]},
		http.StatusConflict, "batch_split_empties_source"); err != nil {
		return err
	}
	// The split-out batches and the source batch can be cancelled independently.
	if _, err := cancelBatch(client, baseURL, split.Batch.ID); err != nil {
		return err
	}
	if _, err := cancelBatch(client, baseURL, secondSplit.Batch.ID); err != nil {
		return err
	}
	if _, err := cancelBatch(client, baseURL, draftBatchID); err != nil {
		return err
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

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL, "PPS-2026-001")
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	return postProgress(client, baseURL, planID, contracts.ProgressRequest{
		Stage:      stage,
		Percent:    percent,
		Note:       "本地链路检查",
		RecordedBy: "preparator-chen",
	})
}

func batchMemberSpec(catalog string) contracts.BatchMemberSpec {
	return contracts.BatchMemberSpec{
		CatalogNumber:  catalog,
		FieldLabel:     "北坡-07",
		TaxonHint:      "大型脊椎动物",
		MatrixKind:     "粉砂岩",
		JacketNotes:    "夹克边角完好",
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       "可逆树脂",
		TargetExposure: 80,
	}
}

func createBatchRequest(catalogPrefix string) contracts.CreateBatchRequest {
	return contracts.CreateBatchRequest{
		Site:    "北坡七号坑",
		Purpose: "同坑多标本修复",
		Owner:   "lead-guo",
		Members: []contracts.BatchMemberSpec{
			batchMemberSpec(catalogPrefix + "A"),
			batchMemberSpec(catalogPrefix + "B"),
			batchMemberSpec(catalogPrefix + "C"),
		},
	}
}

func createOpenBatch(client *http.Client, baseURL, catalogPrefix string) (string, []string, error) {
	snapshot, err := createBatchRaw(client, baseURL, createBatchRequest(catalogPrefix), http.StatusCreated, "")
	if err != nil {
		return "", nil, err
	}
	return snapshot.Batch.ID, memberPlanIDs(snapshot), nil
}

func createApprovedBatch(client *http.Client, baseURL, catalogPrefix string) (string, []string, error) {
	batchID, planIDs, err := createOpenBatch(client, baseURL, catalogPrefix)
	if err != nil {
		return "", nil, err
	}
	if _, err := approveBatch(client, baseURL, batchID); err != nil {
		return "", nil, err
	}
	return batchID, planIDs, nil
}

func memberPlanIDs(snapshot batchSnapshot) []string {
	ids := make([]string, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		ids = append(ids, member.Plan.ID)
	}
	return ids
}

func createBatchRaw(client *http.Client, baseURL string, body contracts.CreateBatchRequest, wantStatus int, wantCode string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches", body)
	if err != nil {
		return snapshot, err
	}
	if status != wantStatus {
		return snapshot, fmt.Errorf("create batch returned %d: %s", status, string(raw))
	}
	if wantStatus == http.StatusCreated {
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return snapshot, err
		}
		return snapshot, nil
	}
	var bodyError errorBody
	if err := json.Unmarshal(raw, &bodyError); err != nil {
		return snapshot, err
	}
	if wantCode != "" && bodyError.Code != wantCode {
		return snapshot, fmt.Errorf("expected error code %s, got %s", wantCode, bodyError.Code)
	}
	return snapshot, nil
}

func approveBatch(client *http.Client, baseURL, batchID string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	err := requestJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/approval",
		contracts.BatchApprovalRequest{ApprovedBy: "curator-lin"}, http.StatusOK, &snapshot)
	return snapshot, err
}

func recordBatchStage(client *http.Client, baseURL, batchID string, body contracts.RecordBatchStageRequest, wantStatus int) (struct {
	Fact struct {
		ID       int    `json:"id"`
		Sequence int    `json:"sequence"`
		Stage    string `json:"stage"`
	} `json:"fact"`
	Entries []struct {
		PlanID  string `json:"plan_id"`
		Percent int    `json:"percent"`
	} `json:"entries"`
}, error) {
	var view struct {
		Fact struct {
			ID       int    `json:"id"`
			Sequence int    `json:"sequence"`
			Stage    string `json:"stage"`
		} `json:"fact"`
		Entries []struct {
			PlanID  string `json:"plan_id"`
			Percent int    `json:"percent"`
		} `json:"entries"`
	}
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/stages", body)
	if err != nil {
		return view, err
	}
	if status != wantStatus {
		return view, fmt.Errorf("record batch stage returned %d: %s", status, string(raw))
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		return view, err
	}
	return view, nil
}

func recordBatchStageExpect(client *http.Client, baseURL, batchID string, body contracts.RecordBatchStageRequest, wantStatus int, wantCode string) error {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/stages", body)
	if err != nil {
		return err
	}
	return expectError(status, raw, wantStatus, wantCode)
}

func postProgress(client *http.Client, baseURL, planID string, body contracts.ProgressRequest) error {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/plans/"+planID+"/progress", body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("progress returned %d: %s", status, string(raw))
	}
	return nil
}

func postProgressStatus(client *http.Client, baseURL, planID string, body contracts.ProgressRequest) int {
	status, _, err := doJSON(client, http.MethodPost, baseURL+"/plans/"+planID+"/progress", body)
	if err != nil {
		return 0
	}
	return status
}

func getBatch(client *http.Client, baseURL, batchID string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	err := requestJSON(client, http.MethodGet, baseURL+"/prep-batches/"+batchID, nil, http.StatusOK, &snapshot)
	return snapshot, err
}

func closeBatch(client *http.Client, baseURL, batchID string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	err := requestJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/close", nil, http.StatusOK, &snapshot)
	return snapshot, err
}

func closeBatchExpect(client *http.Client, baseURL, batchID string, wantStatus int, wantCode string) error {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/close", nil)
	if err != nil {
		return err
	}
	return expectError(status, raw, wantStatus, wantCode)
}

func cancelBatch(client *http.Client, baseURL, batchID string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	err := requestJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/cancel", nil, http.StatusOK, &snapshot)
	return snapshot, err
}

func cancelBatchExpect(client *http.Client, baseURL, batchID string, wantStatus int, wantCode string) error {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/cancel", nil)
	if err != nil {
		return err
	}
	return expectError(status, raw, wantStatus, wantCode)
}

func splitBatch(client *http.Client, baseURL, batchID string, planIDs []string) (batchSnapshot, error) {
	var snapshot batchSnapshot
	err := requestJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/split",
		contracts.SplitBatchRequest{PlanIDs: planIDs}, http.StatusCreated, &snapshot)
	return snapshot, err
}

func splitBatchExpect(client *http.Client, baseURL, batchID string, planIDs []string, wantStatus int, wantCode string) error {
	status, raw, err := doJSON(client, http.MethodPost, baseURL+"/prep-batches/"+batchID+"/split",
		contracts.SplitBatchRequest{PlanIDs: planIDs})
	if err != nil {
		return err
	}
	return expectError(status, raw, wantStatus, wantCode)
}

func expectBatchStatus(client *http.Client, baseURL, batchID, want string) error {
	snapshot, err := getBatch(client, baseURL, batchID)
	if err != nil {
		return err
	}
	if snapshot.Batch.Status != want {
		return fmt.Errorf("expected batch %s, got %s", want, snapshot.Batch.Status)
	}
	return nil
}

func specimenForPlan(client *http.Client, baseURL, planID string) (string, error) {
	var snapshot planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+planID, nil, http.StatusOK, &snapshot); err != nil {
		return "", err
	}
	return snapshot.Plan.SpecimenID, nil
}

func expectError(status int, raw []byte, wantStatus int, wantCode string) error {
	if status != wantStatus {
		return fmt.Errorf("expected status %d, got %d: %s", wantStatus, status, string(raw))
	}
	var bodyError errorBody
	if err := json.Unmarshal(raw, &bodyError); err != nil {
		return err
	}
	if bodyError.Code != wantCode {
		return fmt.Errorf("expected error code %s, got %s", wantCode, bodyError.Code)
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
