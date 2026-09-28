package service

import "testing"

func TestCandyAnswerCorrect(t *testing.T) {
	for _, answer := range []string{"21", "21个", "21 颗糖果。", "答案是 21 个糖果。", "最终答案: **21**", "至少需要21个", "最少需要取出21个糖果。", "二十一颗糖果", "２１个", "The answer is 21.", "21 candies"} {
		if !CandyAnswerCorrect(answer) {
			t.Errorf("rejected correct answer %q", answer)
		}
	}
	for _, answer := range []string{"", "20", "121", "210", "21.5", "-21", "21或22", "不是21", "答案不是21个", "21个不够", "错误答案21", "2 1", "2*1", "21/22"} {
		if CandyAnswerCorrect(answer) {
			t.Errorf("accepted incorrect answer %q", answer)
		}
	}
}
