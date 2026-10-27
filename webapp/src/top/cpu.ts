export function isCPUHog(cpu: number | undefined, cores: number | undefined): boolean {
  return cpu !== undefined && cores !== undefined && cores > 0 && cpu > 25 * cores;
}
