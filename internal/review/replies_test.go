package review

import "testing"

const replies = "# Ответы на ревью T-057\n\n## c3\nstatus: fixed\ncommit: 4e1d0aa\n\nОбернул через fmt.Errorf.\n\n## c5\nstatus: question\n\nОборачивать второй раз?\n"

func TestRepliesAreReadSectionBySection(t *testing.T) {
	t.Parallel()
	got := ParseReplies(replies)
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if r := got[0]; r.ID != "c3" || r.Status != "fixed" || r.Commit != "4e1d0aa" || r.Text != "Обернул через fmt.Errorf." || !r.Parsed || r.Partial {
		t.Fatalf("first = %+v", r)
	}
	if r := got[1]; r.ID != "c5" || r.Status != "question" || r.Text != "Оборачивать второй раз?" {
		t.Fatalf("second = %+v", r)
	}
}

func TestASectionThePanelCannotReadIsShownRaw(t *testing.T) {
	t.Parallel()
	got := ParseReplies("## c3\nstatus: done-ish\n\ntext\n")
	if len(got) != 1 || got[0].Parsed || got[0].Raw == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAFileCutMidWriteMarksOnlyItsLastSection(t *testing.T) {
	t.Parallel()
	got := ParseReplies("## c3\nstatus: fixed\n\nwhole\n\n## c4\nstatus: fix")
	if len(got) != 2 || got[0].Partial || !got[1].Partial {
		t.Fatalf("got %+v", got)
	}
}

// "##c4" is not a heading: it stays text of the section above, and no reply
// claims c4. validate_review.py is what tells the agent so.
func TestAHeadingWithoutASpaceAnswersNothing(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"##c4", "###c4", "### c4"} {
		got := ParseReplies("## c3\nstatus: fixed\n\nx\n\n" + bad + "\nstatus: fixed\n\ny\n")
		if len(got) != 1 || got[0].ID != "c3" {
			t.Fatalf("%s: got %+v", bad, got)
		}
	}
}

func TestASecondAnswerToTheSameCommentIsKeptInOrder(t *testing.T) {
	t.Parallel()
	got := ParseReplies("## c3\nstatus: question\n\nq\n\n## c3\nstatus: fixed\n\nf\n")
	if len(got) != 2 || got[0].Status != "question" || got[1].Status != "fixed" {
		t.Fatalf("got %+v", got)
	}
}
