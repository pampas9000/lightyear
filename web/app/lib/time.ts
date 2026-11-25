// Time helpers. Every value is a Temporal value: the frontend has no other
// date type (see .agent/rules/code-rules.md; `task check:web` enforces it). Pages format
// through `useTime()`, which supplies the i18n locale, the viewer's time
// zone and the shared clock; these functions take them explicitly.

/** useState keys of the shared clock (epoch milliseconds) and the viewer's time zone; see plugins/clock.ts. */
export const CLOCK_NOW_KEY = "clock:now";
export const CLOCK_ZONE_KEY = "clock:zone";

/** The locale and time zone a value is shown in. */
export interface TimeContext {
    locale: string;
    timeZone: string;
}

/** An API timestamp (RFC 3339, e.g. `2026-11-25T10:00:00.123456Z`) or an Instant. */
export type InstantInput = string | Temporal.Instant;

export const toInstant = (value: InstantInput): Temporal.Instant =>
    typeof value === "string" ? Temporal.Instant.from(value) : value;

/** Whether a time zone ID is one this runtime knows. */
export const isValidTimeZone = (timeZone: string): boolean => {
    try {
        Temporal.Instant.fromEpochMilliseconds(0).toZonedDateTimeISO(timeZone);
        return true;
    } catch {
        return false;
    }
};

// Formatting goes through Intl.DateTimeFormat with the instant's epoch
// milliseconds rather than Temporal's toLocaleString: engines' Temporal
// formatting still differs from each other and from the polyfill (V8 gives
// "2026/11/20" where Intl gives "2026年11月20日"), and the server and the
// browser must produce the same text for hydration.
const formatters = new Map<string, Intl.DateTimeFormat>();
const formatter = (locale: string, options: Intl.DateTimeFormatOptions) => {
    const key = `${locale}|${JSON.stringify(options)}`;
    let f = formatters.get(key);
    if (!f) {
        f = new Intl.DateTimeFormat(locale, options);
        formatters.set(key, f);
    }
    return f;
};

/** Formats an instant in the context's locale and time zone. */
export const formatInstant = (
    value: InstantInput,
    ctx: TimeContext,
    options: Intl.DateTimeFormatOptions,
): string =>
    formatter(ctx.locale, { ...options, timeZone: ctx.timeZone }).format(toInstant(value).epochMilliseconds);

/** Formats a calendar date (`YYYY-MM-DD`), which has no time zone. */
export const formatPlainDate = (
    value: string,
    locale: string,
    options: Intl.DateTimeFormatOptions,
): string =>
    formatter(locale, { ...options, timeZone: "UTC" }).format(
        Temporal.PlainDate.from(value).toZonedDateTime("UTC").epochMilliseconds,
    );

/** Whole seconds from `from` to `to`, never negative. */
export const elapsedSeconds = (from: InstantInput, to: InstantInput): number =>
    Math.max(0, Math.floor(toInstant(to).since(toInstant(from)).total("seconds")));

/** A length of time such as "12s" or "3m 4s", in the locale's words. */
export const formatDuration = (totalSeconds: number, locale: string): string => {
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    const parts = minutes > 0 ? { minutes, seconds } : { seconds };
    if (typeof Intl.DurationFormat === "function") {
        return new Intl.DurationFormat(locale, { style: "narrow" }).format(parts);
    }
    return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
};

/**
 * How long ago `value` was, relative to `now`: `justNow` under a minute,
 * then minutes and hours ("5 min. ago"), and the date from a day on.
 */
export const formatRelative = (
    value: InstantInput,
    now: Temporal.Instant,
    ctx: TimeContext,
    justNow: string,
): string => {
    const seconds = elapsedSeconds(value, now);
    if (seconds < 60) return justNow;
    const rtf = new Intl.RelativeTimeFormat(ctx.locale, { numeric: "auto", style: "short" });
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return rtf.format(-minutes, "minute");
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return rtf.format(-hours, "hour");
    return formatInstant(value, ctx, { dateStyle: "medium" });
};
