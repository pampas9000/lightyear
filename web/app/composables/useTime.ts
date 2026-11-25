import {
    CLOCK_NOW_KEY,
    CLOCK_ZONE_KEY,
    elapsedSeconds,
    formatDuration,
    formatInstant,
    formatPlainDate,
    formatRelative,
    type InstantInput,
    type TimeContext,
} from "~/lib/time";

/**
 * Time formatting in the current i18n locale and the viewer's time
 * zone. Every function reads reactive state, so text rendered with it follows
 * a language switch and the shared clock.
 */
export const useTime = () => {
    const { locale, t } = useI18n();
    const nowMs = useState<number>(CLOCK_NOW_KEY);
    const zone = useState<string>(CLOCK_ZONE_KEY);

    const ctx = computed<TimeContext>(() => ({ locale: locale.value, timeZone: zone.value }));
    const now = computed(() => Temporal.Instant.fromEpochMilliseconds(nowMs.value));

    /** Formats an instant; `timeZone` overrides the viewer's zone (e.g. "UTC" for usage periods). */
    const format = (value: InstantInput, options: Intl.DateTimeFormatOptions, timeZone?: string) =>
        formatInstant(value, timeZone ? { ...ctx.value, timeZone } : ctx.value, options);

    return {
        now,
        format,
        /** "Nov 25, 2026, 6:00 PM" */
        dateTime: (value: InstantInput) => format(value, { dateStyle: "medium", timeStyle: "short" }),
        /** "Nov 25, 2026" */
        date: (value: InstantInput) => format(value, { dateStyle: "medium" }),
        /** "06:00 PM" */
        time: (value: InstantInput) => format(value, { hour: "2-digit", minute: "2-digit" }),
        /** A calendar date (`YYYY-MM-DD`), which has no time zone. */
        plainDate: (value: string, options: Intl.DateTimeFormatOptions) =>
            formatPlainDate(value, locale.value, options),
        /** "just now", "5 min. ago", "3 hr. ago", then the date. */
        relative: (value: InstantInput) => formatRelative(value, now.value, ctx.value, t("common.just_now")),
        /** The time from `from` to `to` (or to now) as "12s" or "3m 4s". */
        duration: (from: InstantInput, to?: InstantInput) =>
            formatDuration(elapsedSeconds(from, to ?? now.value), locale.value),
    };
};
