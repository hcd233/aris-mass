"use client";

import type { CSSProperties } from "react";
import { ProviderIcon } from "@/components/provider-icon";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useT } from "@/lib/i18n";
import { PORTAL_CLIENTS, PORTAL_VENDORS, type PortalEntity } from "@/lib/portal";
import { Reveal } from "./reveal";

/** 一行品牌标识：重复铺满框内宽度，两份首尾相接。 */
function LogoRow({
  items,
  label,
  reverse,
  repeat,
  duration,
}: {
  items: readonly PortalEntity[];
  label: string;
  reverse?: boolean;
  repeat: number;
  duration: string;
}) {
  const t = useT();
  const run = Array.from({ length: repeat }, () => items).flat();
  return (
    <div
      className="portal-marquee py-1.5 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      role="group"
      aria-label={label}
      tabIndex={0}
    >
      <div
        className="portal-marquee-track"
        data-reverse={reverse || undefined}
        style={{ "--marquee-duration": duration } as CSSProperties}
      >
        {[0, 1].map((copy) => (
          <ul
            key={copy}
            className="flex shrink-0 gap-6 pr-6"
            aria-hidden={copy === 1 || undefined}
            inert={copy === 1 || undefined}
          >
            {run.map((e, i) => (
              <li
                key={`${e.id}-${i}`}
                aria-hidden={(copy === 0 && i >= items.length) || undefined}
                className="flex h-9 items-center gap-2.5 text-sm font-medium tracking-tight whitespace-nowrap text-foreground"
              >
                <ProviderIcon decorative protocol={e.icon} size={22} />
                {e.labelKey ? t(e.labelKey) : e.label}
              </li>
            ))}
          </ul>
        ))}
      </div>
    </div>
  );
}

export function PortalEcosystem() {
  const t = useT();
  return (
    <Reveal as="section" className="portal-ecosystem mx-auto max-w-6xl px-6 pt-16 pb-10 sm:px-10">
      <p className="mb-6 text-center text-sm text-muted-foreground">{t("portal.eco.title")}</p>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 md:gap-6">
        <section
          className="portal-ecosystem-panel min-w-0"
          aria-labelledby="portal-ecosystem-clients"
        >
          <Card className="h-full rounded-3xl py-6">
            <CardHeader className="px-6">
              <CardTitle>
                <h3 id="portal-ecosystem-clients">{t("portal.eco.clients")}</h3>
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-1.5 px-0">
              <LogoRow
                items={PORTAL_CLIENTS.slice(0, 3)}
                label={t("portal.eco.clients")}
                repeat={3}
                duration="28s"
              />
              <LogoRow
                items={PORTAL_CLIENTS.slice(3)}
                label={t("portal.eco.clients")}
                repeat={3}
                duration="34s"
                reverse
              />
            </CardContent>
          </Card>
        </section>
        <section
          className="portal-ecosystem-panel min-w-0"
          aria-labelledby="portal-ecosystem-vendors"
        >
          <Card className="h-full rounded-3xl py-6">
            <CardHeader className="px-6">
              <CardTitle>
                <h3 id="portal-ecosystem-vendors">{t("portal.eco.vendors")}</h3>
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-1.5 px-0">
              <LogoRow
                items={PORTAL_VENDORS.slice(0, 3)}
                label={t("portal.eco.vendors")}
                repeat={3}
                duration="34s"
                reverse
              />
              <LogoRow
                items={PORTAL_VENDORS.slice(3)}
                label={t("portal.eco.vendors")}
                repeat={3}
                duration="28s"
              />
            </CardContent>
          </Card>
        </section>
      </div>
    </Reveal>
  );
}
