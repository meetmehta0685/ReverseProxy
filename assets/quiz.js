document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-answer]");
  if (!button) return;

  const quiz = button.closest(".quiz");
  const feedback = quiz.querySelector(".quiz-feedback");
  const isCorrect = button.dataset.answer === "correct";

  quiz.querySelectorAll("[data-answer]").forEach((option) => {
    option.disabled = true;
  });
  button.classList.add(isCorrect ? "correct" : "incorrect");
  feedback.textContent = isCorrect
    ? `Correct. ${quiz.dataset.correct}`
    : `Not yet. ${quiz.dataset.incorrect}`;
});
