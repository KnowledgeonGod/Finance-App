export function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

export function money(cents, currency) {
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency,
      maximumFractionDigits: 2,
    }).format(cents / 100);
  } catch {
    return `${currency} ${(cents / 100).toFixed(2)}`;
  }
}

export function moneyGroups(groups, { signed = false } = {}) {
  const entries = [...groups.entries()].filter(([, amount]) => amount !== 0);
  if (entries.length === 0) return "—";
  return entries
    .map(([currency, amount]) => {
      const value = money(Math.abs(amount), currency);
      if (!signed || amount === 0) return amount < 0 ? `−${value}` : value;
      return `${amount > 0 ? "+" : "−"}${value}`;
    })
    .join(" · ");
}

export function formatDate(value) {
  const date = new Date(`${value}T12:00:00`);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" }).format(date);
}

export function transactionRow(transaction, categoryName = "Uncategorized", actions = false) {
  const row = element("div", "transaction-row");
  const main = element("div", "transaction-main");
  const icon = element("span", `transaction-icon ${transaction.kind}`, transaction.kind === "income" ? "↙" : "↗");
  icon.setAttribute("aria-hidden", "true");
  const details = element("div", "transaction-details");
  details.append(
    element("strong", "", transaction.description || categoryName),
    element("span", "", `${categoryName} · ${formatDate(transaction.occurred_on)}`),
  );
  main.append(icon, details);

  const amountSide = element("div", "transaction-side");
  amountSide.append(
    element(
      "span",
      `transaction-amount ${transaction.kind}`,
      `${transaction.kind === "income" ? "+" : "−"}${money(transaction.amount_cents, transaction.currency)}`,
    ),
  );
  if (actions) {
    const buttons = element("div", "transaction-actions");
    const edit = element("button", "row-action", "Edit");
    edit.type = "button";
    edit.dataset.editId = transaction.id;
    edit.setAttribute("aria-label", `Edit ${transaction.description || "transaction"}`);
    const remove = element("button", "row-action row-action-delete", "Delete");
    remove.type = "button";
    remove.dataset.deleteId = transaction.id;
    remove.setAttribute("aria-label", `Delete ${transaction.description || "transaction"}`);
    buttons.append(edit, remove);
    amountSide.append(buttons);
  }
  row.append(main, amountSide);
  return row;
}

export function budgetProgress(budget, spent, label) {
  const percent = budget.amount_cents > 0 ? (spent / budget.amount_cents) * 100 : 0;
  const capped = Math.min(100, Math.max(0, percent));
  const row = element("div", "budget-row");
  const top = element("div", "budget-row-top");
  top.append(
    element("span", "budget-row-name", label),
    element("span", "budget-row-amount", `${money(spent, budget.currency)} / ${money(budget.amount_cents, budget.currency)}`),
  );
  const track = element("div", "progress-track");
  track.setAttribute("role", "progressbar");
  track.setAttribute("aria-valuemin", "0");
  track.setAttribute("aria-valuemax", "100");
  track.setAttribute("aria-valuenow", String(Math.round(percent)));
  track.setAttribute("aria-label", `${label} budget progress`);
  const fill = element("div", `progress-fill${percent >= 80 ? " warning" : ""}`);
  fill.style.width = `${capped}%`;
  track.append(fill);
  row.append(top, track, element("div", "budget-progress-foot", `${Math.round(percent)}% used`));
  return row;
}

export function cashflowChart(income, expense, currency) {
  const container = document.createDocumentFragment();
  const max = Math.max(income, expense, 1);
  for (const [label, amount, kind] of [
    ["INCOME", income, "income"],
    ["EXPENSES", expense, "expense"],
  ]) {
    const group = element("div", "chart-bar-group");
    const bar = element("div", `chart-bar ${kind}`);
    const height = amount === 0 ? 4 : Math.max(12, Math.round((amount / max) * 128));
    bar.style.height = `${height}px`;
    bar.append(element("span", "chart-bar-value", money(amount, currency)));
    group.append(bar);
    container.append(group);
  }
  return container;
}

export function reportLine(label, amount, max, currency, kind) {
  const line = element("div", "report-line");
  const track = element("div", "report-line-track");
  const fill = element("div", `report-line-fill ${kind}`);
  fill.style.width = `${amount === 0 ? 0 : Math.max(3, (amount / max) * 100)}%`;
  track.append(fill);
  line.append(
    element("span", "", label),
    track,
    element("span", "report-line-value", money(amount, currency)),
  );
  return line;
}
