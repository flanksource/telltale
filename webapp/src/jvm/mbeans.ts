export interface MBeanLeaf { objectName: string; label: string; metadata: string }
export interface MBeanDomain { domain: string; count: number; types: Array<{ type: string; mbeans: MBeanLeaf[] }> }

export function parseMBeanArgument(raw: string, type: string): unknown {
  const primitive = type.replace(/^java\.lang\./, "").toLowerCase();
  if (primitive === "string" || type === "javax.management.ObjectName") return raw;
  if (primitive === "boolean") {
    if (raw.trim() === "true") return true;
    if (raw.trim() === "false") return false;
    throw new Error("Expected true or false");
  }
  if (primitive === "char" || primitive === "character") {
    if (raw.length !== 1) throw new Error("Expected one character");
    return raw;
  }
  const bounds: Record<string, [bigint, bigint]> = {
    byte: [-128n, 127n], short: [-32768n, 32767n],
    int: [-2147483648n, 2147483647n], integer: [-2147483648n, 2147483647n],
    long: [-9223372036854775808n, 9223372036854775807n],
  };
  if (bounds[primitive]) {
    if (!/^-?\d+$/.test(raw.trim())) throw new Error(`Expected a ${type} integer`);
    const value = BigInt(raw.trim());
    const [min, max] = bounds[primitive];
    if (value < min || value > max) throw new Error(`Value is outside the ${type} range`);
    return raw.trim();
  }
  if (primitive === "float" || primitive === "double") {
    if (!raw.trim() || !Number.isFinite(Number(raw))) throw new Error(`Expected a finite ${type}`);
    return raw.trim();
  }
  return raw;
}

function splitProperties(objectName: string, value: string): string[] {
  const properties: string[] = [];
  let quoted = false;
  let escaped = false;
  let start = 0;
  for (let index = 0; index < value.length; index++) {
    const character = value[index];
    if (escaped) escaped = false;
    else if (character === "\\" && quoted) escaped = true;
    else if (character === '"') quoted = !quoted;
    else if (character === "," && !quoted) { properties.push(value.slice(start, index).trim()); start = index + 1; }
  }
  if (quoted || escaped) throw new Error(`Invalid MBean ObjectName "${objectName}": unterminated quoted value`);
  properties.push(value.slice(start).trim());
  return properties;
}

function displayValue(value: string): string { return value.startsWith('"') && value.endsWith('"') ? value.slice(1, -1) : value; }

function parseMBeanLeaf(objectName: string) {
  const separator = objectName.indexOf(":");
  if (separator <= 0 || separator === objectName.length - 1) throw new Error(`Invalid MBean ObjectName "${objectName}": expected domain:key=value`);
  const properties = splitProperties(objectName, objectName.slice(separator + 1)).map(raw => {
    const assignment = raw.indexOf("=");
    if (assignment <= 0) throw new Error(`Invalid MBean ObjectName "${objectName}": expected key=value property`);
    return { key: raw.slice(0, assignment), value: raw.slice(assignment + 1), raw };
  });
  const typeProperty = ["type", "j2eeType", "ObjectType", "Type"].map(key => properties.find(property => property.key === key)).find(Boolean);
  const nameProperty = properties.find(property => property.key === "name") ?? properties.find(property => property.key.toLowerCase() === "name");
  const remaining = properties.filter(property => property !== typeProperty && property !== nameProperty);
  const identity = nameProperty ?? remaining[0];
  const type = typeProperty ? displayValue(typeProperty.value) : "Untyped";
  return { domain: objectName.slice(0, separator), type, leaf: { objectName, label: displayValue(identity?.value ?? "") || type, metadata: remaining.filter(property => property !== identity).map(property => property.raw).join(", ") } };
}

export function buildMBeanBrowserTree(names: string[]): MBeanDomain[] {
  const domains = new Map<string, Map<string, MBeanLeaf[]>>();
  for (const objectName of names) {
    const parsed = parseMBeanLeaf(objectName);
    const types = domains.get(parsed.domain) ?? new Map<string, MBeanLeaf[]>();
    types.set(parsed.type, [...(types.get(parsed.type) ?? []), parsed.leaf]);
    domains.set(parsed.domain, types);
  }
  return [...domains.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([domain, types]) => ({
    domain, count: [...types.values()].reduce((total, mbeans) => total + mbeans.length, 0),
    types: [...types.entries()].sort(([left], [right]) => left === right ? 0 : left === "Untyped" ? 1 : right === "Untyped" ? -1 : left.localeCompare(right)).map(([type, mbeans]) => ({ type, mbeans: mbeans.sort((left, right) => left.label.localeCompare(right.label) || left.objectName.localeCompare(right.objectName)) })),
  }));
}
