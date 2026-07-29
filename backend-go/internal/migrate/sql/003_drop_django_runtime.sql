-- 清掉 Django 运行时自带的表。
--
-- 这批表只服务于 Django 自身（迁移记账、admin、session、内建 auth、celery beat、
-- simplejwt 黑名单），业务侧没有任何外键指向它们——权限与组织是 iam_* 那一套。
-- Django 退役后留着它们，只会让后来的人以为系统还依赖某个已经不存在的框架。
--
-- 顺序无所谓：CASCADE 会带掉彼此之间的外键。
DROP TABLE IF EXISTS accounts_user_user_permissions CASCADE;
DROP TABLE IF EXISTS accounts_user_groups CASCADE;
DROP TABLE IF EXISTS auth_group_permissions CASCADE;
DROP TABLE IF EXISTS auth_permission CASCADE;
DROP TABLE IF EXISTS auth_group CASCADE;
DROP TABLE IF EXISTS django_admin_log CASCADE;
DROP TABLE IF EXISTS django_content_type CASCADE;
DROP TABLE IF EXISTS django_session CASCADE;
DROP TABLE IF EXISTS django_migrations CASCADE;
DROP TABLE IF EXISTS django_celery_beat_periodictask CASCADE;
DROP TABLE IF EXISTS django_celery_beat_periodictasks CASCADE;
DROP TABLE IF EXISTS django_celery_beat_crontabschedule CASCADE;
DROP TABLE IF EXISTS django_celery_beat_intervalschedule CASCADE;
DROP TABLE IF EXISTS django_celery_beat_solarschedule CASCADE;
DROP TABLE IF EXISTS django_celery_beat_clockedschedule CASCADE;
DROP TABLE IF EXISTS token_blacklist_blacklistedtoken CASCADE;
DROP TABLE IF EXISTS token_blacklist_outstandingtoken CASCADE;
