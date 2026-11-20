package com.flanksource.telltale.diag.fixture;

/**
 * A superclass with no no-argument constructor, which is the case that makes a generated skeleton
 * non-trivial: {@code ActivityDal extends BaseDal} compiles only if the skeleton emits an explicit
 * {@code super(...)}.
 */
public abstract class SkeletonBase {

  private final String id;
  private final int size;

  protected SkeletonBase(String id, int size) {
    this.id = id;
    this.size = size;
  }

  public String getId() {
    return id;
  }

  public int getSize() {
    return size;
  }
}
