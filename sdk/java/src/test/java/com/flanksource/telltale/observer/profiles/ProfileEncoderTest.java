package com.flanksource.telltale.observer.profiles;

import static org.junit.Assert.*;

import io.opentelemetry.proto.common.v1.AnyValue;
import io.opentelemetry.proto.profiles.v1development.*;
import java.util.*;
import java.nio.file.*;
import org.junit.Test;

public class ProfileEncoderTest {
  @Test
  public void carriesActiveAndEmptySnapshotsWithoutRoundingIntegers() throws Exception {
    Map<String, Object> snapshot = snapshot();
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("operationId", "request-a");
    row.put("elapsedNs", 250000000L);
    row.put("sampledAtNanos", 1794657600000000000L);
    row.put("attributes", Collections.singletonMap("threadId", 9007199254740993L));
    row.put("traceId", "0102030405060708090a0b0c0d0e0f10");
    row.put("spanId", "0102030405060708");
    row.put("stack", Arrays.asList("application.handle", "application.run"));
    snapshot.put("rows", Collections.singletonList(row));
    ProfilesData decoded = ProfilesData.parseFrom(ProfileEncoder.encode(snapshot));
    assertEquals(2L, attributes(decoded, decoded.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0).getAttributeIndicesList())
        .get("telltale.schema.version").getIntValue());
    assertEquals(1, decoded.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0).getSamplesCount());
    assertEquals(250000000L, decoded.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0).getSamples(0).getValues(0));
    assertTrue(decoded.getDictionary().getAttributeTableList().stream()
        .anyMatch(attribute -> attribute.getValue().hasIntValue() && attribute.getValue().getIntValue() == 9007199254740993L));
    snapshot.put("sequence", 2L);
    snapshot.put("observed", 0);
    snapshot.put("rows", Collections.emptyList());
    decoded = ProfilesData.parseFrom(ProfileEncoder.encode(snapshot));
    assertControl(decoded, (Long) snapshot.get("completedAtNanos"));
    assertEquals("", decoded.getDictionary().getStringTable(0));
  }

  @Test(expected = IllegalArgumentException.class)
  public void rejectsMissingProducerIdentity() throws Exception {
    ProfileEncoder.encode(new LinkedHashMap<>());
  }

  @Test
  public void carriesClearingAndIncompleteEmptySnapshotsForBothKinds() throws Exception {
    for (String kind : Arrays.asList("http_top", "sql_top")) {
      for (boolean complete : Arrays.asList(true, false)) {
        Map<String, Object> snapshot = snapshot();
        snapshot.put("kind", kind);
        snapshot.put("observed", 0);
        snapshot.put("rows", Collections.emptyList());
        snapshot.put("complete", complete);
        snapshot.put("dropped", 3L);
        byte[] encoded = ProfileEncoder.encode(snapshot);
        ProfilesData decoded = ProfilesData.parseFrom(encoded);
        assertControl(decoded, (Long) snapshot.get("completedAtNanos"));
        Map<String, AnyValue> metadata = attributes(decoded, decoded.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0).getAttributeIndicesList());
        assertEquals(2L, metadata.get("telltale.schema.version").getIntValue());
        assertEquals(kind, metadata.get("telltale.kind").getStringValue());
        assertEquals(complete, metadata.get("telltale.snapshot.complete").getBoolValue());
        assertEquals(3L, metadata.get("telltale.snapshot.dropped").getIntValue());
        assertArrayEquals(encoded, ProfileEncoder.encode(snapshot));
      }
    }
  }

  @Test(expected = IllegalArgumentException.class)
  public void rejectsEmptyRowsWithPositiveObservedCount() {
    Map<String, Object> snapshot = snapshot();
    snapshot.put("rows", Collections.emptyList());
    ProfileEncoder.encode(snapshot);
  }

  @Test(expected = IllegalArgumentException.class)
  public void rejectsTheControlAttributeOnAnOperation() {
    Map<String, Object> snapshot = snapshot();
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("operationId", "request-a");
    row.put("elapsedNs", 250000000L);
    row.put("sampledAtNanos", snapshot.get("completedAtNanos"));
    row.put("attributes", Collections.singletonMap("telltale.sample.kind", "snapshot_control"));
    snapshot.put("rows", Collections.singletonList(row));
    ProfileEncoder.encode(snapshot);
  }

  @Test
  public void matchesJavaToGoActiveUpdatedAndEmptyGoldenProfiles() throws Exception {
    Map<String, Object> snapshot = snapshot();
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("operationId", "request-a");
    row.put("sampledAtNanos", 1794657600000000000L);
    Map<String, Object> attributes = new LinkedHashMap<>();
    attributes.put("method", "GET");
    attributes.put("url", "https://example.test/orders");
    attributes.put("route", "/orders/{id}");
    attributes.put("direction", "incoming");
    attributes.put("startedAt", "2026-11-14T11:59:59Z");
    attributes.put("threadId", 9007199254740993L);
    row.put("attributes", attributes);
    row.put("traceId", "0102030405060708090a0b0c0d0e0f10");
    row.put("spanId", "0102030405060708");
    row.put("stack", Arrays.asList("application.handle", "application.run"));
    snapshot.put("resource", Collections.singletonMap("service.name", "checkout"));
    String[] names = {"active", "updated", "empty"};
    String[] ids = {"10f5dd68-2656-4c07-abd7-bc884147e020", "20f5dd68-2656-4c07-abd7-bc884147e020", "30f5dd68-2656-4c07-abd7-bc884147e020"};
    for (int index = 0; index < names.length; index++) {
      snapshot.put("profileId", ids[index]);
      snapshot.put("sequence", (long) index + 1);
      snapshot.put("pollIntervalMs", 5000L);
      row.put("elapsedNs", (long) (index + 1) * 250000000L);
      snapshot.put("rows", index == 2 ? Collections.emptyList() : Collections.singletonList(row));
      snapshot.put("observed", index == 2 ? 0 : 1);
      byte[] encoded = ProfileEncoder.encode(snapshot);
      Path fixture = Paths.get("../../pkg/otlp/profiles/testdata/java-http-top-" + names[index] + ".pb");
      if ("true".equals(System.getenv("TELLTALE_UPDATE_PROFILE_GOLDEN"))) {
        Files.createDirectories(fixture.getParent());
        Files.write(fixture, encoded);
      }
      assertArrayEquals(Files.readAllBytes(fixture), encoded);
    }
  }

  private static Map<String, Object> snapshot() {
    Map<String, Object> snapshot = new LinkedHashMap<>();
    snapshot.put("kind", "http_top");
    snapshot.put("captureId", "capture-a");
    snapshot.put("source", "jvm-a");
    snapshot.put("profileId", "10f5dd68-2656-4c07-abd7-bc884147e020");
    snapshot.put("sequence", 1L);
    snapshot.put("startedAtNanos", 1794657600000000000L);
    snapshot.put("completedAtNanos", 1794657600001000000L);
    snapshot.put("observed", 1);
    snapshot.put("limit", 50);
    snapshot.put("dropped", 0L);
    snapshot.put("complete", true);
    return snapshot;
  }

  private static void assertControl(ProfilesData data, long completedAt) {
    Profile profile = data.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    assertEquals(1, profile.getSamplesCount());
    Sample control = profile.getSamples(0);
    assertEquals(Collections.singletonList(0L), control.getValuesList());
    assertEquals(Collections.singletonList(completedAt), control.getTimestampsUnixNanoList());
    assertEquals(0, control.getStackIndex());
    assertEquals(0, control.getLinkIndex());
    assertEquals(Collections.singletonMap("telltale.sample.kind", AnyValue.newBuilder().setStringValue("snapshot_control").build()),
        attributes(data, control.getAttributeIndicesList()));
    assertEquals(0L, attributes(data, profile.getAttributeIndicesList()).get("telltale.snapshot.observed").getIntValue());
    assertEquals(1, data.getDictionary().getStackTableCount());
    assertEquals(1, data.getDictionary().getLinkTableCount());
  }

  private static Map<String, AnyValue> attributes(ProfilesData data, List<Integer> indices) {
    Map<String, AnyValue> attributes = new LinkedHashMap<>();
    for (int index : indices) {
      KeyValueAndUnit attribute = data.getDictionary().getAttributeTable(index);
      assertNull(attributes.put(data.getDictionary().getStringTable(attribute.getKeyStrindex()), attribute.getValue()));
    }
    return attributes;
  }
}
