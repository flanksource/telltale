package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * The order a searched page comes back in. A picker shows the head of that page, so a class named
 * exactly what was typed has to be in it however many package paths and inner classes also contain
 * the text.
 */
final class ClassIndexSearchRankingTest {

  private static final ClassLoader APP = new ClassLoader(null) {};
  private static final ClassLoader SIDECAR = new ClassLoader(null) {};

  private static ClassIndex index(ClassIndex.Entry... entries) {
    return new ClassIndex(new ArrayList<>(Arrays.asList(entries)));
  }

  private static ClassIndex.Entry app(String name) {
    return new ClassIndex.Entry(name, APP);
  }

  @SuppressWarnings("unchecked")
  private static List<String> rows(Map<String, Object> page) {
    List<String> rows = new ArrayList<>();
    for (Map<String, Object> row : (List<Map<String, Object>>) page.get("classes")) {
      rows.add(row.get("name") + "@" + row.get("loader"));
    }
    return rows;
  }

  private static List<String> names(Map<String, Object> page) {
    List<String> names = new ArrayList<>();
    for (String row : rows(page)) {
      names.add(row.substring(0, row.indexOf('@')));
    }
    return names;
  }

  @Test
  void ranksAnExactSimpleNameThenAPrefixThenAContainingNameThenAPackageOnlyMatch() {
    ClassIndex index =
        index(
            app("example.application.pas.activitybll.legacy.PolicyValuesBll"),
            app("example.application.pas.bll.IntakeActivityBll"),
            app("example.application.pas.bll.ActivityBllHelper"),
            app("example.application.pas.bll.ActivityBll"));

    Map<String, Object> page = index.page(null, "activitybll", ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(
        Arrays.asList(
            "example.application.pas.bll.ActivityBll",
            "example.application.pas.bll.ActivityBllHelper",
            "example.application.pas.bll.IntakeActivityBll",
            "example.application.pas.activitybll.legacy.PolicyValuesBll"),
        names(page));
  }

  // ActivityBll$1 starts with the query too; an anonymous class is almost never
  // what was meant, so within a rank it follows the named classes.
  @Test
  void putsInnerClassesAfterNamedClassesOfTheSameRank() {
    ClassIndex index =
        index(
            app("example.application.pas.bll.ActivityBll$1"),
            app("example.application.pas.bll.ActivityBllHelper"),
            app("example.application.pas.bll.ActivityBll$ProcessContext"));

    Map<String, Object> page = index.page(null, "ActivityBll", ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(
        Arrays.asList(
            "example.application.pas.bll.ActivityBllHelper",
            "example.application.pas.bll.ActivityBll$1",
            "example.application.pas.bll.ActivityBll$ProcessContext"),
        names(page));
  }

  @Test
  void keepsTheSameNameInTwoLoadersAdjacentInLoaderOrder() {
    String rule = "example.application.pas.rule.MathRuleEvaluator";
    ClassIndex index = index(new ClassIndex.Entry(rule, SIDECAR), new ClassIndex.Entry(rule, APP));
    String first =
        ClassIndex.hash(APP).compareTo(ClassIndex.hash(SIDECAR)) < 0
            ? ClassIndex.hash(APP)
            : ClassIndex.hash(SIDECAR);
    String second =
        first.equals(ClassIndex.hash(APP)) ? ClassIndex.hash(SIDECAR) : ClassIndex.hash(APP);

    Map<String, Object> page = index.page(null, "MathRule", ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(Arrays.asList(rule + "@" + first, rule + "@" + second), rows(page));
  }

  // The ranked head is what paging walks, so the window is cut after ranking.
  @Test
  void cutsTheWindowFromTheRankedOrder() {
    ClassIndex index =
        index(
            app("example.application.a.ZActivityDal"),
            app("example.application.b.YActivityDal"),
            app("example.application.pas.dal.ActivityDal"));

    Map<String, Object> page = index.page(null, "ActivityDal", 1, 0, false);

    assertEquals(3, page.get("total"));
    assertEquals(Arrays.asList("example.application.pas.dal.ActivityDal"), names(page));
  }

  // Without a query there is nothing to rank against, and the class browser
  // lists a whole loader in name order.
  @Test
  void keepsNameOrderWhenThereIsNoQuery() {
    ClassIndex index =
        index(
            app("example.application.pas.dal.ActivityDal"),
            app("example.application.pas.bll.ActivityBll$1"),
            app("example.application.pas.bll.ActivityBll"));

    Map<String, Object> page = index.page(null, "  ", ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(
        Arrays.asList(
            "example.application.pas.bll.ActivityBll",
            "example.application.pas.bll.ActivityBll$1",
            "example.application.pas.dal.ActivityDal"),
        names(page));
  }
}
