/**
 * The fan-out schematic: PayMux's own topology, drawn with live numbers.
 *
 * Every other view answers a question about one record. This one answers the
 * question the product exists for: one gateway account feeds many
 * applications, so *which branch is broken?* An aggregate cannot say: five
 * healthy applications hide the sixth whose deliveries are dying.
 *
 * It is drawn as a wiring diagram rather than a chart. Orthogonal connectors,
 * not curves: this is an instrument panel, and the shape carries no meaning
 * beyond "signal flows this way".
 */

import { Link } from 'react-router-dom';

import type { Overview } from '../api/types';

/** One application's row in the schematic. */
type Branch = NonNullable<Overview['applications']>[number];

/** How many branches are drawn before the rest are summarised. */
const MAX_BRANCHES = 6;

const ROW = 58;
const TOP = 30;
const GATEWAY_X = 8;
const HUB_X = 300;
const BRANCH_X = 620;
const NODE_W = 190;
const HUB_W = 176;
const CANVAS_W = 900;

type Health = 'ok' | 'busy' | 'broken' | 'idle';

function branchHealth(app: Branch): Health {
  if (app.deliveries_dead > 0 || app.deliveries_failed > 0) return 'broken';
  if (app.pending > 0) return 'busy';
  if (app.payments > 0 || app.deliveries_ok > 0) return 'ok';
  return 'idle';
}

export function FanOut({ overview }: { overview: Overview }) {
  const apps = overview.applications ?? [];
  const shown = apps.slice(0, MAX_BRANCHES);
  const hidden = apps.length - shown.length;

  // The diagram grows with the branches; a fixed height would either crop
  // applications or leave a void.
  const rows = Math.max(shown.length, 1);
  const hubY = TOP + ((rows - 1) * ROW) / 2 + 14;
  // The last branch needs room for its label, not a whole further row. With one
  // application the hub is the tallest thing on the canvas, so it sets the floor.
  const height =
    Math.max(TOP + (rows - 1) * ROW + 30, hubY + 32) + (hidden > 0 ? 26 : 8);

  const broken = apps.filter((a) => branchHealth(a) === 'broken').length;
  // An unattributed notification is a fault on the gateway link itself: the
  // callback arrived and verified, but names an order PayMux never created.
  const gatewayHealth: Health = overview.unrouted_notifications > 0 ? 'broken' : 'ok';
  const dead = apps.reduce((n, a) => n + a.deliveries_dead, 0);
  const failing = apps.reduce((n, a) => n + a.deliveries_failed, 0);

  return (
    <section className="fanout" aria-label="Payment routing">
      <header className="fanout__head">
        <h2>Routing</h2>
        <span className="fanout__caption">{summarise(overview, broken)}</span>
      </header>

      <div className="fanout__canvas">
        {/*
          * Sized in absolute units, then capped at the panel width by CSS.
          * On a wide screen it draws at 1:1 and leaves air to its right rather
          * than stretching: scaling an SVG scales its type with it, and labels
          * larger than the surrounding interface stop reading as an instrument.
          * Below that width it scales down proportionally, which is the one
          * place growing type the other way is the right answer.
          */}
        <svg
          viewBox={`0 0 ${CANVAS_W} ${height}`}
          width={CANVAS_W}
          height={height}
          role="img"
          aria-label={
            broken > 0
              ? `${broken} of ${apps.length} applications are not receiving events`
              : `${apps.length} applications, all receiving events`
          }
        >
          {/* Gateway: where money and notifications originate. */}
          <g>
            <rect
              className="fanout__node fanout__node--gateway"
              x={GATEWAY_X}
              y={hubY - 20}
              width={NODE_W}
              height={40}
              rx="5"
            />
            <text className="fanout__label" x={GATEWAY_X + 14} y={hubY - 3}>
              midtrans
            </text>
            <text className="fanout__sub" x={GATEWAY_X + 14} y={hubY + 12}>
              {overview.unrouted_notifications > 0
                ? `${overview.unrouted_notifications} unattributed`
                : 'notifications verified'}
            </text>
          </g>

          {/* Gateway to hub. */}
          <path
            className={
              overview.unrouted_notifications > 0
                ? 'fanout__wire fanout__wire--broken'
                : 'fanout__wire fanout__wire--ok'
            }
            d={`M ${GATEWAY_X + NODE_W} ${hubY} H ${HUB_X}`}
          />

          {/* PayMux itself. */}
          <g>
            <rect
              className="fanout__node fanout__node--hub"
              x={HUB_X}
              y={hubY - 26}
              width={HUB_W}
              height={52}
              rx="5"
            />
            <text className="fanout__label fanout__label--hub" x={HUB_X + 14} y={hubY - 6}>
              PayMux
            </text>
            <text className="fanout__sub" x={HUB_X + 14} y={hubY + 11}>
              {overview.payments.created} payment{overview.payments.created === 1 ? '' : 's'} ·{' '}
              {overview.payments.paid} paid
            </text>
          </g>

          {shown.length === 0 && (
            <text className="fanout__sub" x={BRANCH_X} y={hubY + 4}>
              no applications yet
            </text>
          )}

          {shown.map((app, index) => {
            const y = TOP + index * ROW + 14;
            const health = branchHealth(app);
            const mid = HUB_X + HUB_W + 26;

            return (
              <g key={app.application_id}>
                {/* Orthogonal connector: out, along, in. */}
                <path
                  className={`fanout__wire fanout__wire--${health}`}
                  d={`M ${HUB_X + HUB_W} ${hubY} H ${mid} V ${y} H ${BRANCH_X}`}
                  fill="none"
                />
                <circle className={`fanout__port fanout__port--${health}`} cx={BRANCH_X} cy={y} r="3.5" />

                <text className="fanout__branch" x={BRANCH_X + 14} y={y - 3}>
                  {app.name}
                </text>
                <text className={`fanout__sub fanout__sub--${health}`} x={BRANCH_X + 14} y={y + 12}>
                  {describeBranch(app)}
                </text>
              </g>
            );
          })}

          {hidden > 0 && (
            <text className="fanout__sub" x={BRANCH_X + 14} y={TOP + shown.length * ROW + 8}>
              and {hidden} more
            </text>
          )}
        </svg>
      </div>

      {/*
        * The same topology for a phone. Scaling the diagram down would take
        * its 13px labels to 6px, so below the breakpoint it reflows into a
        * tree instead: the gateway feeds the hub, the hub fans out to the
        * branches, and the connector into each branch carries its health. The
        * question the schematic exists to answer survives the reflow.
        */}
      <ul className="fanout__stack">
        <li className={`fanout__step fanout__step--gateway fanout__step--${gatewayHealth}`}>
          <span className={`fanout__step-dot fanout__step-dot--${gatewayHealth}`} />
          <span className="fanout__step-name mono">midtrans</span>
          <span className={`fanout__step-note fanout__sub--${gatewayHealth}`}>
            {overview.unrouted_notifications > 0
              ? `${overview.unrouted_notifications} unattributed`
              : 'verified'}
          </span>
        </li>

        <li className="fanout__step fanout__step--hub">
          <span className="fanout__step-dot fanout__step-dot--hub" />
          <span className="fanout__step-name fanout__step-name--hub">PayMux</span>
          <span className="fanout__step-note">
            {overview.payments.created} payment{overview.payments.created === 1 ? '' : 's'} ·{' '}
            {overview.payments.paid} paid
          </span>
        </li>

        {shown.length === 0 && (
          <li className="fanout__step fanout__step--branch fanout__step--idle">
            <span className="fanout__step-dot fanout__step-dot--idle" />
            <span className="fanout__step-note">no applications yet</span>
          </li>
        )}

        {shown.map((app) => {
          const health = branchHealth(app);
          return (
            <li
              key={app.application_id}
              className={`fanout__step fanout__step--branch fanout__step--${health}`}
            >
              <span className={`fanout__step-dot fanout__step-dot--${health}`} />
              <span className="fanout__step-name">{app.name}</span>
              <span className={`fanout__step-note fanout__sub--${health}`}>
                {describeBranch(app)}
              </span>
            </li>
          );
        })}

        {hidden > 0 && (
          <li className="fanout__step fanout__step--branch fanout__step--idle">
            <span className="fanout__step-dot fanout__step-dot--idle" />
            <span className="fanout__step-note">and {hidden} more</span>
          </li>
        )}
      </ul>

      {(dead > 0 || failing > 0) && (
        <footer className="fanout__foot">
          <span className="fanout__foot-text">
            {dead > 0 && `${count(dead, 'delivery', 'deliveries')} gave up`}
            {dead > 0 && failing > 0 && ', '}
            {failing > 0 && `${count(failing, 'delivery', 'deliveries')} still retrying`}
          </span>
          <Link to={dead > 0 ? '/deliveries?state=dead' : '/deliveries?state=failed'}>
            Open deliveries
          </Link>
        </footer>
      )}
    </section>
  );
}

/**
 * One line for the whole diagram, naming the most pressing thing wrong.
 *
 * Unattributed notifications are called out separately from failing branches:
 * they are a different problem with a different fix, and reporting "all
 * healthy" while the gateway link is red would contradict the picture.
 */
function summarise(overview: Overview, broken: number): string {
  const parts: string[] = [];
  if (broken > 0) {
    parts.push(`${broken} application${broken === 1 ? '' : 's'} not receiving events`);
  }
  if (overview.unrouted_notifications > 0) {
    parts.push(
      `${overview.unrouted_notifications} notification${
        overview.unrouted_notifications === 1 ? '' : 's'
      } matched no payment`,
    );
  }
  return parts.length > 0 ? parts.join(' · ') : 'every application is receiving its events';
}

/**
 * One line per branch, saying the most useful thing that is true of it.
 *
 * Ordered by what an operator needs to act on: a delivery that gave up
 * outranks one still retrying, which outranks anything healthy.
 */
function describeBranch(app: Branch): string {
  if (app.deliveries_dead > 0) {
    return `${count(app.deliveries_dead, 'delivery', 'deliveries')} gave up`;
  }
  if (app.deliveries_failed > 0) {
    return `${count(app.deliveries_failed, 'delivery', 'deliveries')} retrying`;
  }
  if (app.pending > 0) {
    return `${count(app.pending, 'payment', 'payments')} awaiting the customer`;
  }
  if (app.deliveries_ok > 0) {
    return `${count(app.deliveries_ok, 'event', 'events')} delivered`;
  }
  if (app.payments > 0) {
    return `${count(app.payments, 'payment', 'payments')}, nothing to deliver yet`;
  }
  return 'quiet';
}

function count(n: number, singular: string, plural: string): string {
  return `${n} ${n === 1 ? singular : plural}`;
}
