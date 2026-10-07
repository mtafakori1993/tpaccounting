// Budget allocation helpers (exact decimals).
import { add, toBig } from './decimal';

/** Percentages of each period in an annual amount; the last share absorbs rounding so they add up to exactly 100. */
export function percentsOf(amounts: string[], annual: string) {
  if (toBig(annual).eq(0)) return amounts.map(() => '');
  const out = amounts.map((a) => toBig(a).times(100).div(toBig(annual)).round(4).toString());
  const rest = toBig(100).minus(toBig(add(...out.slice(0, -1))));
  out[out.length - 1] = rest.toString();
  return out;
}

