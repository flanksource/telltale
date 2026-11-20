package com.flanksource.telltale.observer.profiles;

import com.google.protobuf.ByteString;
import io.opentelemetry.proto.common.v1.AnyValue;
import io.opentelemetry.proto.common.v1.ArrayValue;
import io.opentelemetry.proto.common.v1.KeyValue;
import io.opentelemetry.proto.common.v1.KeyValueList;
import io.opentelemetry.proto.profiles.v1development.*;
import io.opentelemetry.proto.profiles.v1development.Stack;
import java.util.*;

final class ProfileDictionary {
  private final ProfilesDictionary.Builder dictionary = ProfilesDictionary.newBuilder()
      .addStringTable("").addMappingTable(Mapping.getDefaultInstance())
      .addFunctionTable(Function.getDefaultInstance()).addLocationTable(Location.getDefaultInstance())
      .addLinkTable(Link.getDefaultInstance()).addStackTable(Stack.getDefaultInstance())
      .addAttributeTable(KeyValueAndUnit.getDefaultInstance());
  private final Map<String, Integer> strings = new HashMap<>();

  ProfileDictionary() { strings.put("", 0); }

  int text(String value) {
    Integer index = strings.get(value);
    if (index != null) return index;
    int added = dictionary.getStringTableCount();
    dictionary.addStringTable(value);
    strings.put(value, added);
    return added;
  }

  List<Integer> attributes(Map<String, Object> values) {
    List<Integer> indices = new ArrayList<>();
    for (String key : new TreeSet<>(values.keySet())) {
      if (key.isEmpty()) throw new IllegalArgumentException("attribute keys must be nonempty");
      indices.add(dictionary.getAttributeTableCount());
      dictionary.addAttributeTable(KeyValueAndUnit.newBuilder().setKeyStrindex(text(key)).setValue(value(values.get(key))));
    }
    return indices;
  }

  int link(ByteString trace, ByteString span) {
    int index = dictionary.getLinkTableCount();
    dictionary.addLinkTable(Link.newBuilder().setTraceId(trace).setSpanId(span));
    return index;
  }

  int stack(List<?> frames) {
    if (frames.isEmpty()) return 0;
    Stack.Builder stack = Stack.newBuilder();
    for (Object frame : frames) {
      if (!(frame instanceof String) || ((String) frame).isEmpty()) {
        throw new IllegalArgumentException("stack frames must be nonempty strings");
      }
      int function = dictionary.getFunctionTableCount();
      dictionary.addFunctionTable(Function.newBuilder().setNameStrindex(text((String) frame)));
      stack.addLocationIndices(dictionary.getLocationTableCount());
      dictionary.addLocationTable(Location.newBuilder().addLines(Line.newBuilder().setFunctionIndex(function)));
    }
    int index = dictionary.getStackTableCount();
    dictionary.addStackTable(stack);
    return index;
  }

  ProfilesDictionary build() { return dictionary.build(); }

  static AnyValue value(Object value) {
    AnyValue.Builder result = AnyValue.newBuilder();
    if (value == null) return result.build();
    if (value instanceof String) return result.setStringValue((String) value).build();
    if (value instanceof Boolean) return result.setBoolValue((Boolean) value).build();
    if (value instanceof Byte || value instanceof Short || value instanceof Integer || value instanceof Long) {
      return result.setIntValue(((Number) value).longValue()).build();
    }
    if (value instanceof Float || value instanceof Double) return result.setDoubleValue(((Number) value).doubleValue()).build();
    if (value instanceof byte[]) return result.setBytesValue(ByteString.copyFrom((byte[]) value)).build();
    if (value instanceof List) {
      ArrayValue.Builder array = ArrayValue.newBuilder();
      for (Object item : (List<?>) value) array.addValues(value(item));
      return result.setArrayValue(array).build();
    }
    if (value instanceof Map) {
      KeyValueList.Builder entries = KeyValueList.newBuilder();
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) value).entrySet()) {
        if (!(entry.getKey() instanceof String)) throw new IllegalArgumentException("attribute map keys must be strings");
        entries.addValues(KeyValue.newBuilder().setKey((String) entry.getKey()).setValue(value(entry.getValue())));
      }
      return result.setKvlistValue(entries).build();
    }
    throw new IllegalArgumentException("unsupported profile attribute type: " + value.getClass().getName());
  }
}
