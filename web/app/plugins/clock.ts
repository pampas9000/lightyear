import { CLOCK_NOW_KEY, CLOCK_ZONE_KEY, isValidTimeZone } from "~/lib/time";

// The shared clock behind relative times and live durations, and the time
// zone dates are shown in.
//
// The server renders with its own "now" and the zone the browser last stored
// in the `tz` cookie (UTC on a first visit). Both travel in the payload, so
// the browser hydrates with exactly the text the server rendered; once the
// app has mounted it switches to its own zone and starts ticking.
const TICK_MS = 1000;
const ZONE_COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

export default defineNuxtPlugin({
    name: "clock",
    dependsOn: ["temporal"],
    setup(nuxtApp) {
        const zoneCookie = useCookie<string | null>("tz", {
            maxAge: ZONE_COOKIE_MAX_AGE,
            sameSite: "lax",
            path: "/",
        });
        const now = useState<number>(CLOCK_NOW_KEY, () => Temporal.Now.instant().epochMilliseconds);
        const zone = useState<string>(CLOCK_ZONE_KEY, () => {
            const stored = zoneCookie.value;
            return stored && isValidTimeZone(stored) ? stored : "UTC";
        });

        if (import.meta.server) return;

        nuxtApp.hook("app:mounted", () => {
            const local = Temporal.Now.timeZoneId();
            zone.value = local;
            if (zoneCookie.value !== local) zoneCookie.value = local;

            const tick = () => {
                now.value = Temporal.Now.instant().epochMilliseconds;
            };
            tick();
            setInterval(() => {
                if (document.visibilityState === "visible") tick();
            }, TICK_MS);
            document.addEventListener("visibilitychange", () => {
                if (document.visibilityState === "visible") tick();
            });
        });
    },
});
