package main

import "html/template"

// page is the whole storefront: one template, two views, no build step.
//
// A demo whose first instruction is "install these dependencies" demonstrates
// the dependencies. This one is a Go binary and a browser.
var page = template.Must(template.New("page").Funcs(template.FuncMap{
	"rupiah": rupiah,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Shop}}</title>
<style>
  :root {
    --accent: {{.Accent}};
    --ink: #1a1c23;
    --muted: #6b7080;
    --line: #e3e5ec;
    --paper: #fbfbfd;
    --card: #ffffff;
    --ok: #0f7b5f;
    --wait: #9a6300;
    --bad: #b42318;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --ink: #eceef4; --muted: #9ba0b3; --line: #262a36;
      --paper: #0e1016; --card: #171a22;
      --ok: #4ec49b; --wait: #e0a33a; --bad: #f2685c;
    }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--paper); color: var(--ink);
    font: 15px/1.6 ui-sans-serif, system-ui, -apple-system, sans-serif;
  }
  header {
    border-bottom: 1px solid var(--line); background: var(--card);
    padding: 18px 22px; display: flex; align-items: baseline;
    gap: 14px; flex-wrap: wrap;
  }
  h1 { margin: 0; font-size: 20px; letter-spacing: -0.02em; }
  h1 span { color: var(--accent); }
  .tagline { color: var(--muted); font-size: 13px; }
  nav { margin-left: auto; display: flex; gap: 14px; }
  nav a { color: var(--muted); text-decoration: none; font-size: 14px; }
  nav a:hover { color: var(--accent); }
  main { max-width: 860px; margin: 0 auto; padding: 26px 22px 60px; }
  .grid { display: grid; gap: 14px; grid-template-columns: repeat(auto-fill, minmax(230px, 1fr)); }
  .card {
    border: 1px solid var(--line); border-radius: 8px;
    background: var(--card); padding: 16px; display: flex;
    flex-direction: column; gap: 10px;
  }
  .sku { font: 11px ui-monospace, monospace; color: var(--muted); letter-spacing: 0.06em; }
  .name { font-weight: 600; }
  .price { font: 600 17px ui-monospace, monospace; font-variant-numeric: tabular-nums; }
  button {
    margin-top: auto; padding: 9px 14px; border: none; border-radius: 6px;
    background: var(--accent); color: #fff; font-size: 14px;
    font-weight: 500; cursor: pointer;
  }
  button:hover { filter: brightness(1.08); }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th {
    text-align: left; font: 500 11px ui-monospace, monospace;
    letter-spacing: 0.07em; text-transform: uppercase; color: var(--muted);
    padding: 8px 12px 8px 0; border-bottom: 1px solid var(--line);
  }
  td { padding: 10px 12px 10px 0; border-bottom: 1px solid var(--line); vertical-align: top; }
  .mono { font-family: ui-monospace, monospace; font-size: 13px; }
  .num { font-family: ui-monospace, monospace; font-variant-numeric: tabular-nums; text-align: right; }
  .pill {
    display: inline-block; padding: 2px 9px; border-radius: 100px;
    font: 500 12px ui-monospace, monospace;
  }
  .paid { background: color-mix(in srgb, var(--ok) 15%, transparent); color: var(--ok); }
  .waiting { background: color-mix(in srgb, var(--wait) 15%, transparent); color: var(--wait); }
  .failed { background: color-mix(in srgb, var(--bad) 15%, transparent); color: var(--bad); }
  .empty { color: var(--muted); padding: 28px 0; }
  .note {
    margin-top: 26px; padding: 12px 14px; border: 1px solid var(--line);
    border-left: 3px solid var(--accent); border-radius: 6px;
    background: var(--card); color: var(--muted); font-size: 13px;
  }
  @media (max-width: 600px) {
    td, th { padding-right: 8px; }
    .hide-sm { display: none; }
  }
</style>
</head>
<body>
<header>
  <h1>{{.Shop}}<span>.</span></h1>
  <span class="tagline">{{.Tagline}}</span>
  <nav><a href="/">Shop</a><a href="/orders">Orders</a></nav>
</header>
<main>

{{if eq .View "shop"}}
  <div class="grid">
    {{range .Catalogue}}
      <div class="card">
        <span class="sku">{{.SKU}}</span>
        <span class="name">{{.Name}}</span>
        <span class="price">{{rupiah .Price}}</span>
        <form method="post" action="/buy">
          <input type="hidden" name="sku" value="{{.SKU}}">
          <button type="submit">Buy</button>
        </form>
      </div>
    {{end}}
  </div>
  <div class="note">
    Buying sends you to the real Midtrans sandbox. Nothing is charged. When you
    finish paying, this shop hears about it through a signed webhook from
    PayMux, not by watching the browser come back.
  </div>
{{end}}

{{if eq .View "orders"}}
  {{if .Orders}}
    <table>
      <thead>
        <tr>
          <th>Reference</th>
          <th class="hide-sm">Product</th>
          <th class="num">Amount</th>
          <th>Status</th>
          <th class="hide-sm">Payment</th>
        </tr>
      </thead>
      <tbody>
        {{range .Orders}}
          <tr>
            <td class="mono">{{.Reference}}</td>
            <td class="hide-sm">{{.Product}}</td>
            <td class="num">{{rupiah .Amount}}</td>
            <td>
              <span class="pill {{if eq .Status "paid"}}paid{{else if eq .Status "awaiting payment"}}waiting{{else}}failed{{end}}">{{.Status}}</span>
            </td>
            <td class="mono hide-sm">{{if .PaymentID}}{{.PaymentID}}{{else}}—{{end}}</td>
          </tr>
        {{end}}
      </tbody>
    </table>
  {{else}}
    <p class="empty">No orders yet. Buy something and it will appear here once PayMux says it was paid.</p>
  {{end}}
  <div class="note">
    This table is this shop's own record. It only ever contains this shop's
    orders, even though every other shop on this PayMux collects into the same
    merchant account.
  </div>
{{end}}

</main>
</body>
</html>`))
