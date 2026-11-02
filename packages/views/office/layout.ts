import type { AgentCategory, OfficeAgentDesk, OfficeStation, OfficeZoneKey } from "./types";

export const STATION_COORDINATES: Record<OfficeZoneKey, Pick<OfficeStation, "x" | "y" | "width" | "height">> = {
  pm_desk: { x: 420, y: 60, width: 360, height: 160 },
  coding_pods: { x: 0, y: 280, width: 600, height: 400 },
  qa_lab: { x: 600, y: 280, width: 600, height: 400 },
  approval_lounge: { x: 820, y: 60, width: 360, height: 160 },
};

export function stationForCategory(category: AgentCategory): OfficeZoneKey {
  if (category === "management") return "pm_desk";
  if (category === "qa" || category === "security") return "qa_lab";
  return "coding_pods";
}

export function deskCoordinate(stationKey: OfficeZoneKey, index: number): { x: number; y: number } {
  const station = STATION_COORDINATES[stationKey];
  const columns = stationKey === "coding_pods" || stationKey === "qa_lab" ? 2 : 1;
  const column = index % columns;
  const row = Math.floor(index / columns);
  const insetX = columns === 1 ? station.width / 2 : 150;
  const insetY = stationKey === "coding_pods" || stationKey === "qa_lab" ? 80 : station.height / 2;
  const gapX = columns > 1 ? (station.width - insetX * 2) / (columns - 1) : 0;
  return {
    x: station.x + insetX + column * gapX,
    y: station.y + insetY + row * (stationKey === "coding_pods" || stationKey === "qa_lab" ? 230 : 210),
  };
}

export function stationCenter(stationKey: OfficeZoneKey): { x: number; y: number } {
  const station = STATION_COORDINATES[stationKey];
  return { x: station.x + station.width / 2, y: station.y + station.height / 2 };
}

export function stationForDesk(desk: OfficeAgentDesk): OfficeZoneKey {
  return desk.stationKey;
}
