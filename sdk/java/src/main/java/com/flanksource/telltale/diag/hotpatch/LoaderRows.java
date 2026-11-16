package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Builds the {@code /classes/loaders} payload from the classes each loader holds.
 *
 * <p>Separate from {@link ClassIndex} because it answers a different question: the index knows
 * which loader defined what, this knows how to present that as a list somebody has to choose from.
 * The presentation is where the merging happens — families that exist in the thousands collapse to
 * one virtual row each (see {@link LoaderProfile#getGroup()}), and the rows are ordered so
 * application code comes before platform noise.
 */
final class LoaderRows {

  private LoaderRows() {}

  /**
   * @param byLoader class names held by each non-bootstrap loader
   * @param bootstrapNames class names held by the bootstrap loader
   */
  static List<Map<String, Object>> build(
      Map<ClassLoader, List<String>> byLoader, List<String> bootstrapNames) {
    List<Map<String, Object>> rows = new ArrayList<>();
    if (!bootstrapNames.isEmpty()) {
      rows.add(row(ClassIndex.BOOTSTRAP_HASH, "", LoaderProfile.of(null), bootstrapNames));
    }
    Map<String, Group> groups = new LinkedHashMap<>();
    for (Map.Entry<ClassLoader, List<String>> loader : byLoader.entrySet()) {
      LoaderProfile profile = LoaderProfile.of(loader.getKey()).holding(loader.getValue());
      String className = loader.getKey().getClass().getName();
      if (profile.getGroup() == null) {
        rows.add(row(ClassIndex.hash(loader.getKey()), className, profile, loader.getValue()));
        continue;
      }
      Group group = groups.get(profile.getGroup());
      if (group == null) {
        group = new Group(profile, className);
        groups.put(profile.getGroup(), group);
      }
      group.add(loader.getValue());
    }
    for (Map.Entry<String, Group> group : groups.entrySet()) {
      rows.add(group.getValue().row(group.getKey()));
    }
    sort(rows);
    return rows;
  }

  private static void sort(List<Map<String, Object>> rows) {
    Comparator<Map<String, Object>> byKind =
        Comparator.comparingInt(row -> LoaderProfile.rank(String.valueOf(row.get("kind"))));
    Comparator<Map<String, Object>> byCount =
        Comparator.comparingInt(row -> (Integer) row.get("classCount"));
    Comparator<Map<String, Object>> byHash =
        Comparator.comparing(row -> String.valueOf(row.get("hash")));
    rows.sort(byKind.thenComparing(byCount.reversed()).thenComparing(byHash));
  }

  private static Map<String, Object> row(
      String hash, String className, LoaderProfile profile, List<String> classNames) {
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("hash", hash);
    row.put("className", className);
    row.put("label", profile.getLabel());
    row.put("description", LoaderProfile.describe(profile.getKind(), classNames));
    row.put("kind", profile.getKind());
    row.put("classCount", classNames.size());
    return row;
  }

  /** One virtual loader under construction: the members of a merged family. */
  private static final class Group {
    private final LoaderProfile profile;
    private final String className;
    private final List<String> classNames = new ArrayList<>();
    private int loaderCount;

    private Group(LoaderProfile profile, String className) {
      this.profile = profile;
      this.className = className;
    }

    private void add(List<String> names) {
      classNames.addAll(names);
      loaderCount++;
    }

    private Map<String, Object> row(String id) {
      Map<String, Object> row = LoaderRows.row(id, className, profile, classNames);
      // How many loaders were folded together is the whole point of the
      // row, so it belongs in the label rather than only in a field a
      // picker may not render.
      row.put("label", profile.getLabel() + " (" + loaderCount + " loaders)");
      row.put("virtual", Boolean.TRUE);
      row.put("loaderCount", loaderCount);
      return row;
    }
  }
}
