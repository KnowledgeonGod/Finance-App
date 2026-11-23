import { api, getToken, jsonRequest, setToken } from "./api.js";
import { budgetProgress, cashflowChart, element, money, moneyGroups, reportLine, transactionRow } from "./ui.js";

const pageNames = {
  overview: "Overview",
  transactions: "Transactions",
  budgets: "Budgets",
  reports: "Reports",
};
const pageTitles = {
  overview: ["Good to see you.", "Here’s what’s happening with your money this month."],
  transactions: ["Your transactions.", "Every little thing, all in one place."],
  budgets: ["Plan with purpose.", "Keep your monthly spending on track."],
  reports: ["The bigger picture.", "A clear look at your monthly cash flow."],
};
const monthInput = document.querySelector("#month-picker");
const authForm = document.querySelector("#auth-form");
const passwordResetRequestForm = document.querySelector("#password-reset-request-form");
const passwordResetForm = document.querySelector("#password-reset-form");
const authPassword = document.querySelector("#auth-password");
const confirmPassword = document.querySelector("#confirm-password");
const resetPassword = document.querySelector("#reset-password");
const transactionForm = document.querySelector("#transaction-form");
const budgetForm = document.querySelector("#budget-form");
const categoryForm = document.querySelector("#category-form");
const transactionDialog = document.querySelector("#transaction-dialog");
const budgetDialog = document.querySelector("#budget-dialog");
const categoryDialog = document.querySelector("#category-dialog");
const today = new Date();
const currentMonth = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}`;
let selectedMonth = currentMonth;
let currentPage = "overview";
let registering = false;
let resetToken = "";
let transactionOffset = 0;
let transactionID = null;
let recentTransactions = [];
let pageTransactions = [];
let categories = [];
let budgets = [];
let alerts = [];
let report = { month: selectedMonth, totals: [] };
let returnToTransaction = false;
let toastTimer;

const $ = (selector) => document.querySelector(selector);
const setText = (selector, value) => {
  const node = $(selector);
  if (node) node.textContent = value;
};
const setError = (selector, message = "") => {
  const node = $(selector);
  if (!node) return;
  node.textContent = message;
  node.classList.remove("is-success");
};
const setSuccess = (selector, message) => {
  const node = $(selector);
  if (!node) return;
  node.textContent = message;
  node.classList.add("is-success");
};
const categoryName = (id) => categories.find((category) => category.id === id)?.name || "Uncategorized";
const localDate = () => {
  const date = new Date();
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
};
const groupsFor = (kind) => {
  const groups = new Map();
  for (const item of report.totals) {
    if (item.kind === kind) groups.set(item.currency, (groups.get(item.currency) || 0) + item.amount_cents);
  }
  return groups;
};
const countsFor = (kind) => report.totals.filter((item) => item.kind === kind).reduce((sum, item) => sum + item.count, 0);

function showNotice(message = "") {
  const notice = $("#notice");
  notice.textContent = message;
  notice.classList.toggle("hidden", !message);
}

function showToast(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.remove("hidden");
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => toast.classList.add("hidden"), 3000);
}

function replayAnimation(node, className) {
  node.classList.remove(className);
  void node.offsetWidth;
  node.classList.add(className);
}

function animateAuthCopy() {
  replayAnimation($(".auth-copy"), "auth-copy-enter");
}

function showAuth() {
  registering = false;
  resetToken = "";
  $("#app-view").classList.add("hidden");
  $("#auth-view").classList.remove("hidden");
  authForm.reset();
  passwordResetRequestForm.reset();
  passwordResetForm.reset();
  authForm.classList.remove("hidden");
  passwordResetRequestForm.classList.add("hidden");
  passwordResetForm.classList.add("hidden");
  $("#forgot-password-row").classList.remove("hidden");
  $("#auth-switch-row").classList.remove("hidden");
  $("#auth-note").classList.remove("hidden");
  resetPassword.removeAttribute("aria-describedby");
  $("#reset-password-rules").removeAttribute("aria-live");
  confirmPassword.required = false;
  setAuthCopy();
  updatePasswordRules(authPassword, "password");
  setError("#auth-error");
}

function setAuthCopy() {
  $("#auth-title").textContent = registering ? "A fresh start." : "Welcome back.";
  $("#auth-description").textContent = registering
    ? "Create your private account to get started."
    : "Sign in to see how your finances are doing.";
  $("#auth-submit").textContent = registering ? "Create account" : "Sign in";
  $("#auth-switch-copy").textContent = registering ? "Already have an account?" : "New to Ledgerly?";
  $("#auth-switch").textContent = registering ? "Sign in" : "Create an account";
  $("#forgot-password-row").classList.toggle("hidden", registering);
  $("#password-rules").classList.toggle("hidden", !registering);
  if (registering) $("#password-rules").setAttribute("aria-live", "polite");
  else $("#password-rules").removeAttribute("aria-live");
  $("#confirm-password-label").classList.toggle("hidden", !registering);
  if (registering) authPassword.setAttribute("aria-describedby", "password-rules");
  else authPassword.removeAttribute("aria-describedby");
  confirmPassword.required = registering;
  authPassword.autocomplete = registering ? "new-password" : "current-password";
  authPassword.placeholder = registering ? "At least 8 characters" : "Your password";
  animateAuthCopy();
  replayAnimation(authForm, "auth-form-enter");
}

function showPasswordResetRequest() {
  $("#reset-request-email").value = authForm.elements.email.value;
  authForm.classList.add("hidden");
  passwordResetRequestForm.classList.remove("hidden");
  passwordResetForm.classList.add("hidden");
  resetPassword.removeAttribute("aria-describedby");
  $("#password-rules").removeAttribute("aria-live");
  $("#reset-password-rules").removeAttribute("aria-live");
  $("#forgot-password-row").classList.add("hidden");
  $("#auth-switch-row").classList.add("hidden");
  $("#auth-note").classList.add("hidden");
  setText("#auth-title", "Reset your password.");
  setText("#auth-description", "Enter your email and we’ll send a one-time reset link.");
  animateAuthCopy();
  setError("#reset-request-message");
}

function showPasswordReset(token) {
  resetToken = token;
  passwordResetForm.reset();
  authForm.classList.add("hidden");
  passwordResetRequestForm.classList.add("hidden");
  passwordResetForm.classList.remove("hidden");
  resetPassword.setAttribute("aria-describedby", "reset-password-rules");
  $("#password-rules").removeAttribute("aria-live");
  $("#reset-password-rules").setAttribute("aria-live", "polite");
  $("#forgot-password-row").classList.add("hidden");
  $("#auth-switch-row").classList.add("hidden");
  $("#auth-note").classList.add("hidden");
  setText("#auth-title", "Choose a new password.");
  setText("#auth-description", "Create a new password for your account.");
  animateAuthCopy();
  setError("#reset-password-message");
  updatePasswordRules(resetPassword, "reset-password");
}

function showApp() {
  $("#auth-view").classList.add("hidden");
  $("#app-view").classList.remove("hidden");
  setText("#today-label", new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric" }).format(today));
  monthInput.value = selectedMonth;
  renderPage(currentPage);
}

function renderPage(page) {
  currentPage = pageNames[page] ? page : "overview";
  for (const name of Object.keys(pageNames)) {
    $(`#${name}-page`).classList.toggle("hidden", name !== currentPage);
  }
  document.querySelectorAll(".nav-link").forEach((link) => {
    link.classList.toggle("active", link.dataset.page === currentPage);
  });
  setText("#page-breadcrumb", pageNames[currentPage]);
  setText("#page-title", pageTitles[currentPage][0]);
  setText("#page-subtitle", pageTitles[currentPage][1]);
  closeMenu();
  if (currentPage === "transactions" && getToken()) loadTransactions();
}

function closeMenu() {
  $("#sidebar").classList.remove("open");
  $("#sidebar-scrim").classList.remove("show");
  $("#menu-button").setAttribute("aria-expanded", "false");
}

function renderSelectOptions(selector, placeholder, selected = "") {
  const select = $(selector);
  const fragment = document.createDocumentFragment();
  const emptyOption = element("option", "", placeholder);
  emptyOption.value = "";
  fragment.append(emptyOption);
  for (const category of categories) {
    const option = element("option", "", category.name);
    option.value = category.id;
    fragment.append(option);
  }
  select.replaceChildren(fragment);
  select.value = selected;
}

function renderOverview() {
  const income = groupsFor("income");
  const expenses = groupsFor("expense");
  const net = new Map();
  for (const currency of new Set([...income.keys(), ...expenses.keys()])) {
    net.set(currency, (income.get(currency) || 0) - (expenses.get(currency) || 0));
  }
  setText("#income-total", moneyGroups(income));
  setText("#expense-total", moneyGroups(expenses));
  setText("#net-total", moneyGroups(net, { signed: true }));
  setText("#income-count", `${countsFor("income")} transactions`);
  setText("#expense-count", `${countsFor("expense")} transactions`);

  const chartCurrency = report.totals[0]?.currency || "USD";
  const hasMultipleCurrencies = new Set(report.totals.map((item) => item.currency)).size > 1;
  setText("#cashflow-caption", hasMultipleCurrencies
    ? `Cash flow shown in ${chartCurrency}; summary totals are grouped by currency`
    : "Income and expenses this month");
  $("#cashflow-chart").replaceChildren(
    cashflowChart(income.get(chartCurrency) || 0, expenses.get(chartCurrency) || 0, chartCurrency),
  );

  const budgetList = $("#budget-preview");
  budgetList.replaceChildren();
  const monthBudgets = budgets.filter((budget) => budget.month === selectedMonth).slice(0, 3);
  if (monthBudgets.length === 0) {
    budgetList.append(element("div", "empty-state", "No budgets for this month yet."));
  } else {
    for (const budget of monthBudgets) {
      const currentAlert = alerts.find((item) => item.budget_id === budget.id);
      budgetList.append(budgetProgress(
        budget,
        currentAlert?.spent_cents || 0,
        budget.category_id ? categoryName(budget.category_id) : "All categories",
      ));
    }
  }

  const list = $("#recent-transactions");
  list.replaceChildren();
  if (recentTransactions.length === 0) {
    list.append(element("div", "empty-state", "No transactions yet. Add your first one to get started."));
  } else {
    for (const item of recentTransactions) list.append(transactionRow(item, categoryName(item.category_id)));
  }
}

function renderTransactions() {
  const list = $("#all-transactions");
  list.replaceChildren();
  if (pageTransactions.length === 0) {
    list.append(element("div", "empty-state", "No transactions match these filters."));
  } else {
    for (const item of pageTransactions) list.append(transactionRow(item, categoryName(item.category_id), true));
  }
  const page = Math.floor(transactionOffset / 50) + 1;
  setText("#page-status", `Page ${page}`);
  $("#previous-page").disabled = transactionOffset === 0;
  $("#next-page").disabled = pageTransactions.length < 50;
}

function renderBudgets() {
  const alertList = $("#budget-alerts");
  alertList.replaceChildren();
  const budgetGrid = $("#all-budgets");
  budgetGrid.replaceChildren();
  const monthBudgets = budgets.filter((budget) => budget.month === selectedMonth);
  for (const alert of alerts) {
    if (!alert.alert) continue;
    const budget = monthBudgets.find((item) => item.id === alert.budget_id);
    const label = budget?.category_id ? categoryName(budget.category_id) : "Your total budget";
    alertList.append(element("div", "budget-alert", `${label} is ${Math.round(alert.percent_spent)}% used this month.`));
  }
  if (monthBudgets.length === 0) {
    budgetGrid.append(element("div", "empty-state", "No budgets for this month. Create one to start planning."));
    return;
  }
  for (const budget of monthBudgets) {
    const currentAlert = alerts.find((item) => item.budget_id === budget.id);
    const card = element("article", "panel budget-card");
    const heading = element("div", "panel-heading");
    heading.append(
      element("div", "budget-row-name", budget.category_id ? categoryName(budget.category_id) : "All categories"),
      element("span", "budget-row-amount", budget.month),
    );
    card.append(
      heading,
      element("div", "budget-card-value", `${money(budget.amount_cents, budget.currency)} `),
      element("div", "budget-card-sub", `${money(currentAlert?.spent_cents || 0, budget.currency)} spent of ${money(budget.amount_cents, budget.currency)}`),
      budgetProgress(budget, currentAlert?.spent_cents || 0, "Monthly progress"),
    );
    budgetGrid.append(card);
  }
}

function renderReport() {
  const income = groupsFor("income");
  const expenses = groupsFor("expense");
  const net = new Map();
  for (const currency of new Set([...income.keys(), ...expenses.keys()])) {
    net.set(currency, (income.get(currency) || 0) - (expenses.get(currency) || 0));
  }
  setText("#report-income-total", moneyGroups(income));
  setText("#report-expense-total", moneyGroups(expenses));
  setText("#report-net-total", moneyGroups(net, { signed: true }));
  setText("#report-income-count", `${countsFor("income")} transactions`);
  setText("#report-expense-count", `${countsFor("expense")} transactions`);
  setText("#report-month-label", new Intl.DateTimeFormat(undefined, { month: "long", year: "numeric" })
    .format(new Date(`${selectedMonth}-01T12:00:00`)));

  const chart = $("#report-chart");
  chart.replaceChildren();
  const currencies = new Set([...income.keys(), ...expenses.keys()]);
  if (currencies.size === 0) {
    chart.append(element("div", "empty-state", "No activity recorded for this month."));
    return;
  }
  for (const currency of currencies) {
    const monthlyIncome = income.get(currency) || 0;
    const monthlyExpense = expenses.get(currency) || 0;
    const max = Math.max(monthlyIncome, monthlyExpense, 1);
    chart.append(
      element("div", "report-currency-label", currency),
      reportLine("Income", monthlyIncome, max, currency, "income"),
      reportLine("Expenses", monthlyExpense, max, currency, "expense"),
    );
  }
}

async function loadTransactions() {
  if (!getToken()) return;
  const query = new URLSearchParams({ limit: "50", offset: String(transactionOffset) });
  const kind = $("#filter-kind").value;
  const start = $("#filter-start").value;
  const end = $("#filter-end").value;
  if (kind) query.set("kind", kind);
  if (start) query.set("start_date", start);
  if (end) query.set("end_date", end);
  try {
    pageTransactions = await api(`/api/v1/transactions?${query}`);
    renderTransactions();
  } catch (error) {
    showNotice(error.message);
  }
}

async function refreshDashboard() {
  if (!getToken()) return;
  showNotice();
  try {
    const monthQuery = new URLSearchParams({ month: selectedMonth });
    const [categoryData, recentData, reportData, budgetData, alertData] = await Promise.all([
      api("/api/v1/categories"),
      api("/api/v1/transactions?limit=5&offset=0"),
      api(`/api/v1/reports/monthly?${monthQuery}`),
      api("/api/v1/budgets"),
      api(`/api/v1/budgets/alerts?${monthQuery}`),
    ]);
    categories = categoryData;
    recentTransactions = recentData;
    report = reportData;
    budgets = budgetData;
    alerts = alertData;
    renderSelectOptions("#transaction-category", "Uncategorized");
    renderSelectOptions("#budget-category", "All categories");
    renderOverview();
    renderBudgets();
    renderReport();
    await loadTransactions();
  } catch (error) {
    showNotice(error.message);
  }
}

function openTransactionForm(transaction = null) {
  transactionForm.reset();
  transactionID = transaction?.id || null;
  renderSelectOptions("#transaction-category", "Uncategorized", transaction?.category_id || "");
  transactionForm.elements.kind.value = transaction?.kind || "expense";
  transactionForm.elements.amount.value = transaction ? (transaction.amount_cents / 100).toFixed(2) : "";
  transactionForm.elements.currency.value = transaction?.currency || "USD";
  transactionForm.elements.occurred_on.value = transaction?.occurred_on || localDate();
  transactionForm.elements.category_id.value = transaction?.category_id || "";
  transactionForm.elements.description.value = transaction?.description || "";
  setText("#transaction-dialog-title", transactionID ? "Edit transaction" : "Add a transaction");
  setError("#transaction-error");
  transactionDialog.showModal();
}

function openBudgetForm() {
  budgetForm.reset();
  budgetForm.elements.month.value = selectedMonth;
  renderSelectOptions("#budget-category", "All categories");
  setError("#budget-error");
  budgetDialog.showModal();
}

function openCategoryForm() {
  returnToTransaction = transactionDialog.open;
  if (returnToTransaction) transactionDialog.close();
  categoryForm.reset();
  setError("#category-error");
  categoryDialog.showModal();
}

function amountToCents(value) {
  const amount = Number(value);
  if (!Number.isFinite(amount) || amount <= 0) throw new Error("Enter an amount greater than zero.");
  const cents = Math.round(amount * 100);
  if (!Number.isSafeInteger(cents) || cents <= 0) throw new Error("Enter a valid amount.");
  return cents;
}

function updatePasswordRules(input, prefix) {
  const password = input.value;
  const rules = [
    ["length", Array.from(password).length >= 8],
    ["uppercase", /\p{Lu}/u.test(password)],
    ["special", /[\p{P}\p{S}]/u.test(password)],
    ["byte-limit", new TextEncoder().encode(password).length <= 72],
  ];
  for (const [rule, satisfied] of rules) {
    $(`#${prefix}-rule-${rule}`).classList.toggle("is-met", satisfied);
  }
}

async function submitAuth(event) {
  event.preventDefault();
  setError("#auth-error");
  const form = new FormData(authForm);
  if (registering && form.get("password") !== form.get("confirm_password")) {
    setError("#auth-error", "Passwords do not match.");
    return;
  }
  const submit = $("#auth-submit");
  submit.disabled = true;
  try {
    const path = registering ? "/api/v1/auth/register" : "/api/v1/auth/login";
    const response = await api(path, jsonRequest("POST", {
      email: form.get("email"),
      password: form.get("password"),
    }));
    if (registering) {
      registering = false;
      authForm.elements.password.value = "";
      confirmPassword.value = "";
      setAuthCopy();
      setSuccess("#auth-error", response.message);
      return;
    }
    setToken(response.access_token);
    selectedMonth = currentMonth;
    showApp();
    await refreshDashboard();
  } catch (error) {
    setError("#auth-error", error.message);
  } finally {
    submit.disabled = false;
  }
}

async function submitPasswordResetRequest(event) {
  event.preventDefault();
  const submit = passwordResetRequestForm.querySelector('[type="submit"]');
  submit.disabled = true;
  setError("#reset-request-message");
  const form = new FormData(passwordResetRequestForm);
  try {
    const response = await api("/api/v1/auth/password-reset/request", jsonRequest("POST", {
      email: form.get("email"),
    }));
    setSuccess("#reset-request-message", response.message);
  } catch (error) {
    setError("#reset-request-message", error.message);
  } finally {
    submit.disabled = false;
  }
}

async function submitPasswordReset(event) {
  event.preventDefault();
  setError("#reset-password-message");
  const form = new FormData(passwordResetForm);
  if (form.get("password") !== form.get("confirm_password")) {
    setError("#reset-password-message", "Passwords do not match.");
    return;
  }
  const submit = passwordResetForm.querySelector('[type="submit"]');
  submit.disabled = true;
  try {
    await api("/api/v1/auth/password-reset/complete", jsonRequest("POST", {
      token: resetToken,
      password: form.get("password"),
    }));
    showAuth();
    setSuccess("#auth-error", "Password reset. Sign in with your new password.");
  } catch (error) {
    setError("#reset-password-message", error.message);
  } finally {
    submit.disabled = false;
  }
}

async function submitTransaction(event) {
  event.preventDefault();
  setError("#transaction-error");
  const submit = transactionForm.querySelector('[type="submit"]');
  submit.disabled = true;
  const form = new FormData(transactionForm);
  try {
    const payload = {
      kind: form.get("kind"),
      amount_cents: amountToCents(form.get("amount")),
      currency: String(form.get("currency")).trim().toUpperCase(),
      description: String(form.get("description")).trim(),
      occurred_on: form.get("occurred_on"),
      category_id: form.get("category_id") || null,
    };
    const path = transactionID
      ? `/api/v1/transactions/${encodeURIComponent(transactionID)}`
      : "/api/v1/transactions";
    await api(path, jsonRequest(transactionID ? "PUT" : "POST", payload));
    transactionDialog.close();
    await refreshDashboard();
    showToast(transactionID ? "Transaction updated." : "Transaction added.");
    transactionID = null;
  } catch (error) {
    setError("#transaction-error", error.message);
  } finally {
    submit.disabled = false;
  }
}

async function submitBudget(event) {
  event.preventDefault();
  setError("#budget-error");
  const submit = budgetForm.querySelector('[type="submit"]');
  submit.disabled = true;
  const form = new FormData(budgetForm);
  try {
    await api("/api/v1/budgets", jsonRequest("POST", {
      amount_cents: amountToCents(form.get("amount")),
      month: form.get("month"),
      currency: String(form.get("currency")).trim().toUpperCase(),
      category_id: form.get("category_id") || null,
    }));
    budgetDialog.close();
    selectedMonth = form.get("month");
    monthInput.value = selectedMonth;
    await refreshDashboard();
    showToast("Budget created.");
  } catch (error) {
    setError("#budget-error", error.message);
  } finally {
    submit.disabled = false;
  }
}

async function submitCategory(event) {
  event.preventDefault();
  setError("#category-error");
  const submit = categoryForm.querySelector('[type="submit"]');
  submit.disabled = true;
  const form = new FormData(categoryForm);
  try {
    const added = await api("/api/v1/categories", jsonRequest("POST", { name: String(form.get("name")).trim() }));
    categories = await api("/api/v1/categories");
    renderSelectOptions("#transaction-category", "Uncategorized", added.id);
    renderSelectOptions("#budget-category", "All categories");
    categoryDialog.close();
    showToast("Category created.");
  } catch (error) {
    setError("#category-error", error.message);
  } finally {
    submit.disabled = false;
  }
}

document.addEventListener("click", async (event) => {
  const target = event.target instanceof Element ? event.target : null;
  const pageLink = target?.closest("[data-page]");
  const action = target?.closest("[data-action]");
  const editButton = target?.closest("[data-edit-id]");
  const deleteButton = target?.closest("[data-delete-id]");

  if (pageLink) {
    event.preventDefault();
    renderPage(pageLink.dataset.page);
  }
  if (action?.dataset.action === "new-transaction") openTransactionForm();
  if (action?.dataset.action === "new-budget") openBudgetForm();
  if (action?.dataset.action === "new-category") openCategoryForm();
  if (target?.closest("[data-close-dialog]")) target.closest("dialog")?.close();
  if (editButton) {
    const transaction = pageTransactions.find((item) => item.id === editButton.dataset.editId);
    if (transaction) openTransactionForm(transaction);
  }
  if (deleteButton) {
    const transaction = pageTransactions.find((item) => item.id === deleteButton.dataset.deleteId);
    if (!transaction || !window.confirm(`Delete "${transaction.description || "this transaction"}"?`)) return;
    try {
      await api(`/api/v1/transactions/${encodeURIComponent(transaction.id)}`, { method: "DELETE" });
      await refreshDashboard();
      showToast("Transaction deleted.");
    } catch (error) {
      showNotice(error.message);
    }
  }
});

authForm.addEventListener("submit", submitAuth);
passwordResetRequestForm.addEventListener("submit", submitPasswordResetRequest);
passwordResetForm.addEventListener("submit", submitPasswordReset);
transactionForm.addEventListener("submit", submitTransaction);
budgetForm.addEventListener("submit", submitBudget);
categoryForm.addEventListener("submit", submitCategory);
$("#auth-switch").addEventListener("click", () => {
  registering = !registering;
  confirmPassword.value = "";
  setAuthCopy();
  updatePasswordRules(authPassword, "password");
  setError("#auth-error");
});
$("#forgot-password").addEventListener("click", showPasswordResetRequest);
$("#reset-request-back").addEventListener("click", showAuth);
$("#reset-password-back").addEventListener("click", showAuth);
authPassword.addEventListener("input", () => updatePasswordRules(authPassword, "password"));
resetPassword.addEventListener("input", () => updatePasswordRules(resetPassword, "reset-password"));
authForm.addEventListener("input", () => setError("#auth-error"));
$("#logout-button").addEventListener("click", async () => {
  const button = $("#logout-button");
  button.disabled = true;
  try {
    await api("/api/v1/auth/logout", { method: "POST" });
    setToken(null);
    showAuth();
  } catch (error) {
    if (getToken()) showNotice(error.message);
  } finally {
    button.disabled = false;
  }
});
$("#menu-button").addEventListener("click", () => {
  const open = !$("#sidebar").classList.contains("open");
  $("#sidebar").classList.toggle("open", open);
  $("#sidebar-scrim").classList.toggle("show", open);
  $("#menu-button").setAttribute("aria-expanded", String(open));
});
$("#sidebar-scrim").addEventListener("click", closeMenu);
monthInput.addEventListener("change", () => {
  if (!monthInput.value) return;
  selectedMonth = monthInput.value;
  refreshDashboard();
});
$("#apply-filters").addEventListener("click", () => {
  transactionOffset = 0;
  loadTransactions();
});
$("#previous-page").addEventListener("click", () => {
  transactionOffset = Math.max(0, transactionOffset - 50);
  loadTransactions();
});
$("#next-page").addEventListener("click", () => {
  if (pageTransactions.length === 50) {
    transactionOffset += 50;
    loadTransactions();
  }
});
categoryDialog.addEventListener("close", () => {
  if (!returnToTransaction) return;
  returnToTransaction = false;
  transactionDialog.showModal();
});
window.addEventListener("ledgerly:unauthorized", showAuth);

const initialResetToken = new URLSearchParams(window.location.hash.slice(1)).get("reset_token");
const initialVerificationToken = new URLSearchParams(window.location.hash.slice(1)).get("verify_email");
if (initialResetToken || initialVerificationToken) {
  setToken(null);
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
  showAuth();
  if (initialResetToken) {
    showPasswordReset(initialResetToken);
  } else {
    api("/api/v1/auth/email-verification/complete", jsonRequest("POST", {
      token: initialVerificationToken,
    })).then((response) => {
      setSuccess("#auth-error", response.message);
    }).catch((error) => {
      setError("#auth-error", error.message);
    });
  }
} else if (getToken()) {
  showApp();
  refreshDashboard();
} else {
  showAuth();
}
