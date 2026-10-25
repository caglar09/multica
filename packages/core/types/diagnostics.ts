export interface DiagnosticLogSource {
  id: string;
  label: string;
  image?: string;
  state?: string;
  status?: string;
}

export type DiagnosticLogLevel = "debug" | "info" | "warn" | "error";

export interface DiagnosticLogEntry {
  timestamp?: string;
  source: string;
  container?: string;
  stream: "stdout" | "stderr";
  level: DiagnosticLogLevel;
  message: string;
}

export interface DiagnosticLogsResponse {
  collected_at: string;
  sources: DiagnosticLogSource[];
  entries: DiagnosticLogEntry[];
}

export interface DiagnosticLogQuery {
  source?: string;
  search?: string;
  tail?: number;
  since?: number;
}
