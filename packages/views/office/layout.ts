import type { AgentCategory, OfficeAgentDesk, OfficeStation, OfficeZoneKey } from "./types";

export const STATION_COORDINATES: Record<OfficeZoneKey, Pick<OfficeStation, "x" | "y" | "width" | "height">> = {
  pm_desk: { x: 600, y: 140, width: 280, height: 160 },
  coding_pods: { x: 380, y: 480, width: 560, height: 320 },
  qa_lab: { x: 920, y: 480, width: 420, height: 320 },
  approval_lounge: { x: 1050, y: 140, width: 280, height: 160 },
};

export function stationForCategory(category: AgentCategory): OfficeZoneKey {
  if (category === "management") return "pm_desk";
  if (category === "qa" || category === "security") return "qa_lab";
  return "coding_pods";
}

export function deskCoordinate(stationKey: OfficeZoneKey, index: number): { x: number; y: number } {
  const station = STATION_COORDINATES[stationKey];
  const columns = stationKey === "coding_pods" ? 4 : stationKey === "qa_lab" ? 3 : 2;
  const column = index % columns;
  const row = Math.floor(index / columns);
  const insetX = stationKey === "pm_desk" || stationKey === "approval_lounge" ? 70 : 56;
  const insetY = stationKey === "pm_desk" || stationKey === "approval_lounge" ? 72 : 92;
  const gapX = columns > 1 ? (station.width - insetX * 2) / (columns - 1) : 0;
  return { x: station.x + insetX + column * gapX, y: station.y + insetY + row * 82 };
}

export function stationCenter(stationKey: OfficeZoneKey): { x: number; y: number } {
  const station = STATION_COORDINATES[stationKey];
  return { x: station.x + station.width / 2, y: station.y + station.height / 2 };
}

export function stationForDesk(desk: OfficeAgentDesk): OfficeZoneKey {
  return desk.stationKey;
}
