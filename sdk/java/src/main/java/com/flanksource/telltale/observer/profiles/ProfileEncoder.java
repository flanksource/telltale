package com.flanksource.telltale.observer.profiles;

import com.google.protobuf.ByteString;
import io.opentelemetry.proto.common.v1.InstrumentationScope;
import io.opentelemetry.proto.common.v1.KeyValue;
import io.opentelemetry.proto.profiles.v1development.*;
import io.opentelemetry.proto.resource.v1.Resource;
import java.nio.ByteBuffer;
import java.util.*;

public final class ProfileEncoder {
  public static final String CONTENT_TYPE =
      "application/x-protobuf;message=opentelemetry.proto.profiles.v1development.ProfilesData";

  private ProfileEncoder() {}

  public static byte[] encode(Map<String, Object> snapshot) {
    String kind = text(snapshot, "kind");
    if (!kind.equals("http_top") && !kind.equals("sql_top") && !kind.equals("jms_top")) {
      throw new IllegalArgumentException("unsupported top profile kind: " + kind);
    }
    String capture = text(snapshot, "captureId");
    String source = text(snapshot, "source");
    UUID id = UUID.fromString(text(snapshot, "profileId"));
    if (id.equals(new UUID(0, 0))) throw new IllegalArgumentException("profile ID must be nonzero");
    long sequence = integer(snapshot, "sequence");
    long start = integer(snapshot, "startedAtNanos");
    long end = integer(snapshot, "completedAtNanos");
    long observed = integer(snapshot, "observed");
    long limit = integer(snapshot, "limit");
    long dropped = integer(snapshot, "dropped");
    List<?> rows = list(snapshot.get("rows"), "rows");
    if (sequence <= 0 || start <= 0 || end < start || observed < rows.size() || (rows.isEmpty() && observed != 0)
        || limit < 0 || (limit > 0 && rows.size() > limit) || dropped < 0) {
      throw new IllegalArgumentException("invalid snapshot sequence, timestamps or counts");
    }
    if (!(snapshot.get("complete") instanceof Boolean)) {
      throw new IllegalArgumentException("snapshot completeness is required");
    }
    ProfileDictionary dictionary = new ProfileDictionary();
    Profile.Builder profile = Profile.newBuilder().setProfileId(uuid(id)).setTimeUnixNano(start)
        .setDurationNano(end - start).setSampleType(ValueType.newBuilder()
            .setTypeStrindex(dictionary.text("telltale." + kind.substring(0, kind.length() - 4) + ".active.elapsed"))
            .setUnitStrindex(dictionary.text("ns")));
    Map<String, Object> metadata = new TreeMap<>();
    metadata.put("telltale.schema.version", 2L);
    metadata.put("telltale.kind", kind);
    metadata.put("telltale.capture.id", capture);
    metadata.put("telltale.source.instance", source);
    metadata.put("telltale.snapshot.sequence", sequence);
    metadata.put("telltale.snapshot.observed", observed);
    metadata.put("telltale.snapshot.limit", limit);
    metadata.put("telltale.snapshot.dropped", dropped);
    metadata.put("telltale.snapshot.complete", snapshot.get("complete"));
    metadata.put("telltale.checkpoint.id", optionalText(snapshot, "checkpoint"));
    if (snapshot.containsKey("pollIntervalMs")) {
      long poll = integer(snapshot, "pollIntervalMs");
      if (poll <= 0) throw new IllegalArgumentException("poll interval must be positive");
      metadata.put("telltale.snapshot.poll_interval_ms", poll);
    }
    profile.addAllAttributeIndices(dictionary.attributes(metadata));
    // WORKAROUND(collector-empty-profiles): Keep zero-operation snapshots through Collector sample-count guards; approved 2026-11-15.
    // Correct fix: Preserve metadata-only profiles through receiver, queue admission, merging and splitting.
    // Ref: https://github.com/open-telemetry/opentelemetry-collector/blob/v0.163.0/receiver/otlpreceiver/internal/profiles/otlp.go
    if (rows.isEmpty()) profile.addSamples(Sample.newBuilder().addValues(0).addTimestampsUnixNano(end)
        .addAllAttributeIndices(dictionary.attributes(Collections.singletonMap("telltale.sample.kind", "snapshot_control"))));
    Set<String> identities = new HashSet<>();
    for (Object item : rows) {
      Map<String, Object> row = map(item, "row");
      String operation = text(row, "operationId");
      if (!identities.add(operation)) throw new IllegalArgumentException("duplicate operation ID: " + operation);
      profile.addSamples(sample(dictionary, row, start, end));
    }
    Resource.Builder resource = Resource.newBuilder();
    if (snapshot.containsKey("resource")) {
      for (Map.Entry<String, Object> attribute : map(snapshot.get("resource"), "resource").entrySet()) {
        resource.addAttributes(KeyValue.newBuilder().setKey(attribute.getKey())
            .setValue(ProfileDictionary.value(attribute.getValue())));
      }
    }
    return ProfilesData.newBuilder().setDictionary(dictionary.build()).addResourceProfiles(
        ResourceProfiles.newBuilder().setResource(resource).addScopeProfiles(ScopeProfiles.newBuilder()
            .setScope(InstrumentationScope.newBuilder().setName("com.flanksource.telltale.top").setVersion("1"))
            .addProfiles(profile))).build().toByteArray();
  }

  private static Sample sample(ProfileDictionary dictionary, Map<String, Object> row, long start, long end) {
    long elapsed = integer(row, "elapsedNs");
    long sampled = integer(row, "sampledAtNanos");
    if (elapsed < 0 || sampled < start || sampled > end) {
      throw new IllegalArgumentException("sample must have nonnegative elapsed time and bounded timestamp");
    }
    Map<String, Object> attributes = new TreeMap<>();
    if (row.containsKey("attributes")) attributes.putAll(map(row.get("attributes"), "attributes"));
    if (attributes.containsKey("telltale.sample.kind")) throw new IllegalArgumentException("operation attributes must not contain the reserved sample kind");
    attributes.put("telltale.operation.id", text(row, "operationId"));
    Sample.Builder sample = Sample.newBuilder().addValues(elapsed).addTimestampsUnixNano(sampled)
        .addAllAttributeIndices(dictionary.attributes(attributes));
    String trace = optionalText(row, "traceId");
    String span = optionalText(row, "spanId");
    if (!trace.isEmpty() || !span.isEmpty()) sample.setLinkIndex(dictionary.link(hex(trace, 16), hex(span, 8)));
    if (row.containsKey("stack")) sample.setStackIndex(dictionary.stack(list(row.get("stack"), "stack")));
    return sample.build();
  }

  private static ByteString hex(String value, int bytes) {
    if (value.length() != bytes * 2) throw new IllegalArgumentException("invalid trace/span ID length");
    byte[] decoded = new byte[bytes];
    boolean nonzero = false;
    for (int i = 0; i < bytes; i++) {
      int high = Character.digit(value.charAt(i * 2), 16);
      int low = Character.digit(value.charAt(i * 2 + 1), 16);
      if (high < 0 || low < 0) throw new IllegalArgumentException("invalid trace/span ID hex");
      decoded[i] = (byte) ((high << 4) | low);
      nonzero |= decoded[i] != 0;
    }
    if (!nonzero) throw new IllegalArgumentException("trace/span ID must be nonzero");
    return ByteString.copyFrom(decoded);
  }

  private static ByteString uuid(UUID value) {
    return ByteString.copyFrom(ByteBuffer.allocate(16).putLong(value.getMostSignificantBits())
        .putLong(value.getLeastSignificantBits()).array());
  }

  static String text(Map<String, Object> value, String name) {
    String result = optionalText(value, name);
    if (result.isEmpty()) throw new IllegalArgumentException(name + " must be nonempty");
    return result;
  }

  private static String optionalText(Map<String, Object> value, String name) {
    if (!value.containsKey(name)) return "";
    if (!(value.get(name) instanceof String)) throw new IllegalArgumentException(name + " must be a string");
    return (String) value.get(name);
  }

  private static long integer(Map<String, Object> value, String name) {
    Object number = value.get(name);
    if (!(number instanceof Byte || number instanceof Short || number instanceof Integer || number instanceof Long)) {
      throw new IllegalArgumentException(name + " must be an integer");
    }
    return ((Number) number).longValue();
  }

  private static List<?> list(Object value, String name) {
    if (!(value instanceof List)) throw new IllegalArgumentException(name + " must be a list");
    return (List<?>) value;
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> map(Object value, String name) {
    if (!(value instanceof Map)) throw new IllegalArgumentException(name + " must be a map");
    Map<?, ?> values = (Map<?, ?>) value;
    for (Object key : values.keySet()) {
      if (!(key instanceof String)) throw new IllegalArgumentException(name + " keys must be strings");
    }
    return (Map<String, Object>) value;
  }
}
