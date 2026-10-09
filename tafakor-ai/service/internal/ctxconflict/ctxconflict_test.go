package ctxconflict

import "testing"

const antenna = "یه رسید خرید آنتن برای سرایداری شرکت ۱۲۰۰۰۰۰ تومان بزن"

func in(text string, cur *Task, f ...Failure) Input {
	return Input{UserRequest: text, Normalized: Normalize(text), Command: IsCommand(text), Current: cur, Failures: f}
}

func TestSameRequestFailedInContextResets(t *testing.T) {
	r := Detect(in(antenna, &Task{ID: "t1", Stage: "stopped", UserText: antenna}, Failure{TaskID: "t1", Normalized: Normalize(antenna), Outcome: "stopped"}))
	if r.Action != NewContext || r.Severity != SevCritical || r.Reasons[0].Code != SameRequestFailed || !r.ArchitectureBug || r.CausingState == "" {
		t.Fatalf("%+v", r)
	}
}

func TestOneResetPerRequest(t *testing.T) {
	x := in(antenna, nil, Failure{TaskID: "t1", Normalized: Normalize(antenna), Outcome: "stopped"})
	x.ResetsSoFar = 1
	if r := Detect(x); r.Action != Continue || r.Reasons[len(r.Reasons)-1].Code != NoMoreReset {
		t.Fatalf("%+v", r)
	}
}

func TestStoppedTaskThenNewRequestResets(t *testing.T) {
	r := Detect(in(antenna, &Task{ID: "t1", Stage: "stopped", UserText: "حقوق علی رو ۳۰ میلیون کن"}))
	if r.Action != NewContext || r.Reasons[0].Code != UnrecoverableTask {
		t.Fatalf("%+v", r)
	}
	r = Detect(in("مراکز هزینه رو لیست کن", &Task{ID: "t1", Stage: "ui_running", UserText: "x"}))
	if r.Action != NewContext || r.Reasons[0].Code != UnknownExecutionState {
		t.Fatalf("%+v", r)
	}
}

// Normal conversation never resets.
func TestNormalFollowUpsContinue(t *testing.T) {
	cur := &Task{ID: "t1", Stage: "done", Domain: "payroll", UserText: "یه کارمند به اسم علی بساز"}
	for _, s := range []string{"حقوقش رو ۳۰ میلیون کن", "حالا بهش کمک هزینه سفر هم اضافه کن", "مراکز هزینه رو لیست کن", "show me the cost centers", "۵ میلیون", "بله"} {
		if r := Detect(in(s, cur)); r.Action != Continue {
			t.Errorf("%q: %+v", s, r)
		}
	}
	// an answer to an open question is an answer, whatever it says
	x := in("از حساب ملت پرداخت شد", &Task{ID: "t2", Stage: "awaiting_answer", Domain: "accounting", UserText: antenna})
	x.IsAnswer = true
	if r := Detect(x); r.Action != Continue {
		t.Fatalf("%+v", r)
	}
	// a NO_EXECUTOR task is closed (failed): an unrelated request is processed normally
	if r := Detect(in("مراکز هزینه رو لیست کن", &Task{ID: "t3", Stage: "failed", FailureCode: "NO_EXECUTOR", UserText: "x"})); r.Action != Continue {
		t.Fatalf("closed failed task: %+v", r)
	}
}

// Another domain while a question is open: a new task (superseding), not a reset; a
// different entity than the task assumed is flagged, not reset.
func TestDomainAndEntityConflictsAreNotResets(t *testing.T) {
	x := in("برای پروژه وب‌سایت یه هزینه ثبت کن", &Task{ID: "t1", Stage: "awaiting_approval", Domain: "configuration", UserText: "مرکز هزینه عیاشی رو حذف کن",
		Entities: map[string]string{"cost_center": "عیاشی"}})
	x.NewDomain, x.NewEntities = "accounting", map[string]string{"cost_center": "پروژه وب‌سایت"}
	r := Detect(x)
	if r.Action != Continue || !r.Conflict || len(r.Reasons) < 2 {
		t.Fatalf("%+v", r)
	}
}

func TestReferringRequestNeverResets(t *testing.T) {
	x := in("حالا بهش کمک هزینه سفر هم اضافه کن", &Task{ID: "t1", Stage: "stopped", UserText: "یه کارمند به اسم علی بساز"})
	x.Anaphoric = Anaphoric(x.UserRequest)
	if r := Detect(x); r.Action != Continue || !x.Anaphoric {
		t.Fatalf("%+v anaphoric=%v", r, x.Anaphoric)
	}
	for _, s := range []string{"حقوقش رو ۳۰ میلیون کن", "همین رو حذف کن", "pay it now"} {
		if !Anaphoric(s) {
			t.Errorf("%q refers back", s)
		}
	}
	for _, s := range []string{antenna, "مراکز هزینه رو لیست کن", "گزارش فروش رو بگو", "برای پروژه وب‌سایت یه هزینه ثبت کن"} {
		if Anaphoric(s) {
			t.Errorf("%q does not refer back", s)
		}
	}
}
